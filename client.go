package tuyacloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Client talks to the Tuya Cloud OpenAPI. It manages access tokens itself and
// is safe for concurrent use.
type Client struct {
	accessID  string
	accessKey string
	host      string
	http      Doer

	mu       sync.Mutex // guards the cached token
	token    string
	tokenExp time.Time
}

const (
	// defaultTimeout bounds a request when the caller leaves the HTTP client to
	// this package. Tuya is a remote cloud service, so a stuck connection must
	// not wedge a polling loop.
	defaultTimeout = 15 * time.Second

	// tokenLeeway renews the access token a little before it actually expires,
	// so a request never travels with a token that dies in flight.
	tokenLeeway = 60 * time.Second

	// maxErrorBody caps how much of an unparsable response is kept for the
	// error message.
	maxErrorBody = 512

	signMethod = "HMAC-SHA256"
)

// now and nonce are indirections over the two sources of non-determinism in a
// signed request, so tests can pin them.
var (
	now   = time.Now
	nonce = randomNonce
)

// Region is the Tuya data centre serving an app account. Accounts live in
// exactly one, decided by the phone country code they registered with.
type Region string

const (
	// RegionEurope is the Central Europe data centre, which also serves Africa,
	// the Middle East and Central Asia.
	RegionEurope Region = "https://openapi.tuyaeu.com"
	// RegionAmerica is the Western America data centre, the default for regions
	// that are not listed anywhere else.
	RegionAmerica Region = "https://openapi.tuyaus.com"
	// RegionChina is the China data centre.
	RegionChina Region = "https://openapi.tuyacn.com"
	// RegionIndia is the India data centre.
	RegionIndia Region = "https://openapi.tuyain.com"
)

// Option customises a Client.
type Option func(*Client)

// WithRegion selects the data centre to talk to. The default is RegionEurope.
// The region must match the one hosting the linked app account: credentials
// are not valid across data centres.
func WithRegion(r Region) Option {
	return func(c *Client) {
		if r != "" {
			c.host = string(r)
		}
	}
}

// WithHost overrides the API endpoint outright, for a private deployment or a
// test server. It takes precedence over WithRegion when both are given.
func WithHost(host string) Option {
	return func(c *Client) {
		if h := strings.TrimRight(host, "/"); h != "" {
			c.host = h
		}
	}
}

// WithHTTPClient supplies the HTTP client used for every call, which is the
// hook for custom timeouts, proxies, retries or instrumentation.
func WithHTTPClient(d Doer) Option {
	return func(c *Client) {
		if d != nil {
			c.http = d
		}
	}
}

// envelope is the wrapper every Tuya response arrives in: the payload lives in
// result, and success reports whether the platform accepted the call.
type envelope struct {
	Success bool            `json:"success"`
	Code    int             `json:"code"`
	Msg     string          `json:"msg"`
	Result  json.RawMessage `json:"result"`
}

// New creates a client for the given cloud-project credentials. It performs no
// network I/O; the first call fetches an access token.
func New(accessID, accessKey string, opts ...Option) (*Client, error) {
	if accessID == "" || accessKey == "" {
		return nil, errors.New("tuyacloud: access ID and access key are required")
	}
	c := &Client{
		accessID:  accessID,
		accessKey: accessKey,
		host:      string(RegionEurope),
		http:      &http.Client{Timeout: defaultTimeout},
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// Get performs a signed GET against an API path such as "/v1.0/devices/{id}",
// decoding the result field of the response into out. It is the escape hatch
// for endpoints this package does not wrap.
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

// Post performs a signed POST, encoding body as JSON and decoding the result
// field of the response into out. Both may be nil.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, out)
}

// do signs and performs one API call, retrying once with a fresh token if the
// platform reports the current one as invalid.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	payload, err := encodeBody(body)
	if err != nil {
		return err
	}

	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	err = c.call(ctx, method, path, query, payload, token, out)

	// A token can be revoked before it expires, so one retry costs little and
	// turns a spurious failure into a success.
	var apiErr *APIError
	if errors.As(err, &apiErr) && isTokenError(apiErr.Code) {
		if token, err = c.refreshToken(ctx, token); err != nil {
			return err
		}
		return c.call(ctx, method, path, query, payload, token, out)
	}
	return err
}

// call signs and performs a single request with the given token, without any
// token management of its own.
func (c *Client) call(ctx context.Context, method, path string, query url.Values, body []byte, token string, out any) error {
	req, err := c.newRequest(ctx, method, path, query, body, token)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("tuyacloud: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("tuyacloud: %s %s: read response: %w", method, path, err)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status, Path: path, Body: truncate(raw)}
	}
	if !env.Success {
		// Every real Tuya refusal carries a non-zero code; decodable JSON
		// without one is some intermediary's error page, not an envelope.
		if env.Code == 0 {
			return &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status, Path: path, Body: truncate(raw)}
		}
		return &APIError{Code: env.Code, Msg: env.Msg, Path: path}
	}
	if out == nil || len(env.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("tuyacloud: %s %s: decode result: %w", method, path, err)
	}
	return nil
}

// newRequest builds a signed request. Every call carries a fresh timestamp and
// nonce, both of which are part of the signature.
func (c *Client) newRequest(ctx context.Context, method, path string, query url.Values, body []byte, token string) (*http.Request, error) {
	u, err := url.Parse(c.host + path)
	if err != nil {
		return nil, fmt.Errorf("tuyacloud: build URL for %s: %w", path, err)
	}
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("tuyacloud: new %s request for %s: %w", method, path, err)
	}

	timestamp := strconv.FormatInt(now().UnixMilli(), 10)
	n := nonce()
	req.Header.Set("client_id", c.accessID)
	req.Header.Set("sign_method", signMethod)
	req.Header.Set("t", timestamp)
	req.Header.Set("nonce", n)
	if token != "" {
		req.Header.Set("access_token", token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("sign", signature(c.accessID, c.accessKey, token, timestamp, n, method, u, body))
	return req, nil
}

// encodeBody marshals a request body, treating nil as no body at all rather
// than as the JSON literal null, which Tuya rejects.
func encodeBody(body any) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("tuyacloud: encode request body: %w", err)
	}
	return payload, nil
}

// truncate keeps an unparsable response short enough to belong in an error.
func truncate(b []byte) string {
	if len(b) <= maxErrorBody {
		return string(b)
	}
	cut := maxErrorBody
	for cut > maxErrorBody-utf8.UTFMax && !utf8.RuneStart(b[cut]) {
		cut--
	}
	return string(b[:cut]) + "…"
}
