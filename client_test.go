package tuyacloud

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
)

// call is one request the fake platform received.
type call struct {
	method  string
	path    string
	escaped string // path as it arrived on the wire, before decoding
	query   string
	header  http.Header
	body    string
}

// platform is a stand-in for the Tuya cloud: it answers the token endpoint on
// its own and delegates everything else to the case under test.
type platform struct {
	mu    sync.Mutex
	calls []call

	tokenExpire  int64  // token lifetime in seconds
	tokenUID     string // user ID handed out with the token
	tokenReplies []string
	handler      func(w http.ResponseWriter, r *http.Request)
}

// Fixed clock and nonce, so every signature a test produces is reproducible.
var (
	testTime  = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	testNonce = "abcdef0123456789abcdef0123456789"
)

// errTransport is returned by a deliberately broken HTTP client.
var errTransport = errors.New("transport failure")

// newPlatform starts a fake platform and returns it with a client aimed at it.
func newPlatform(t *testing.T, p *platform, opts ...Option) (*Client, *platform) {
	t.Helper()
	if p == nil {
		p = &platform{}
	}
	if p.tokenExpire == 0 {
		p.tokenExpire = 7200
	}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)

	c, err := New("testaccessid", "testsecret", append([]Option{WithHost(srv.URL)}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, p
}

// ServeHTTP verifies the request the way the real platform would, records it,
// then answers it.
func (p *platform) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if reason := verifyRequest(r, body); reason != "" {
		_, _ = fmt.Fprintf(w, `{"success":false,"code":1004,"msg":%q}`, reason)
		return
	}
	p.mu.Lock()
	p.calls = append(p.calls, call{
		method:  r.Method,
		path:    r.URL.Path,
		escaped: r.URL.EscapedPath(),
		query:   r.URL.RawQuery,
		header:  r.Header.Clone(),
		body:    string(body),
	})
	n := len(p.calls)
	p.mu.Unlock()

	if r.URL.Path == tokenPath {
		if reply := p.tokenReply(n); reply != "" {
			_, _ = io.WriteString(w, reply)
			return
		}
		fmt.Fprintf(w, `{"success":true,"result":{"access_token":"tok%d","expire_time":%d,"uid":%q}}`,
			p.tokenCount(), p.tokenExpire, p.tokenUID)
		return
	}
	if p.handler != nil {
		p.handler(w, r)
		return
	}
	_, _ = io.WriteString(w, `{"success":true,"result":{}}`)
}

// verifyRequest checks the headers and signature of a request against Tuya's
// documented rules, reporting the first violation. The signature is recomputed
// by serverSignature, so a request only passes if what travelled on the wire
// was signed correctly — not merely if a sign header is present.
func verifyRequest(r *http.Request, body []byte) string {
	switch {
	case r.Header.Get("client_id") != "testaccessid":
		return "bad client_id"
	case r.Header.Get("sign_method") != "HMAC-SHA256":
		return "bad sign_method"
	case len(r.Header.Get("t")) != 13:
		return "t is not a millisecond timestamp"
	case r.Header.Get("nonce") == "":
		return "missing nonce"
	case r.Header.Get("sign") != serverSignature(r, body):
		return "sign invalid"
	}
	return ""
}

// serverSignature recomputes the expected signature from nothing but the
// received request. It is an independent transcription of the documented
// algorithm — it shares no code with the client — so agreement means the
// client signs what it actually sends.
func serverSignature(r *http.Request, body []byte) string {
	digest := sha256.Sum256(body)

	uri := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return "unparsable query"
		}
		keys := make([]string, 0, len(values))
		for k := range values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(values))
		for _, k := range keys {
			for _, v := range values[k] {
				parts = append(parts, k+"="+v)
			}
		}
		uri += "?" + strings.Join(parts, "&")
	}

	stringToSign := r.Method + "\n" + hex.EncodeToString(digest[:]) + "\n\n" + uri
	mac := hmac.New(sha256.New, []byte("testsecret"))
	mac.Write([]byte("testaccessid" + r.Header.Get("access_token") + r.Header.Get("t") + r.Header.Get("nonce") + stringToSign))
	return strings.ToUpper(hex.EncodeToString(mac.Sum(nil)))
}

// tokenReply returns a canned response for the n-th request, if the case set one.
func (p *platform) tokenReply(n int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.tokenReplies) == 0 {
		return ""
	}
	if n > len(p.tokenReplies) {
		return p.tokenReplies[len(p.tokenReplies)-1]
	}
	return p.tokenReplies[n-1]
}

// tokenCount reports how many tokens have been handed out, so each is distinct.
func (p *platform) tokenCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.calls {
		if c.path == tokenPath {
			n++
		}
	}
	return n
}

// requests returns the recorded calls, excluding token fetches unless asked.
func (p *platform) requests(includeToken bool) []call {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]call, 0, len(p.calls))
	for _, c := range p.calls {
		if includeToken || c.path != tokenPath {
			out = append(out, c)
		}
	}
	return out
}

// pinClock fixes the timestamp and nonce for the duration of a test.
func pinClock(t *testing.T) {
	t.Helper()
	origNow, origNonce := now, nonce
	now = func() time.Time { return testTime }
	nonce = func() string { return testNonce }
	t.Cleanup(func() { now, nonce = origNow, origNonce })
}

// checkErr compares err against the expected message and reports whether the
// case should go on to check the wanted result.
func checkErr(t *testing.T, err error, wantErr string) bool {
	t.Helper()
	switch {
	case wantErr == "" && err != nil:
		t.Fatalf("unexpected error: %v", err)
	case wantErr == "":
		return true
	case err == nil:
		t.Fatalf("expected an error containing %q, got nil", wantErr)
	case !strings.Contains(err.Error(), wantErr):
		t.Errorf("error %q does not contain %q", err, wantErr)
	}
	return false
}

// jsonHandler answers every non-token request with a fixed body.
func jsonHandler(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }
}

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		id, key  string
		opts     []Option
		wantHost string
		wantErr  string
	}{
		{
			name: "defaults to the central europe data centre",
			id:   "id", key: "key",
			wantHost: "https://openapi.tuyaeu.com",
		},
		{
			name: "region selects the data centre",
			id:   "id", key: "key",
			opts:     []Option{WithRegion(RegionAmerica)},
			wantHost: "https://openapi.tuyaus.com",
		},
		{
			name: "an empty region keeps the default",
			id:   "id", key: "key",
			opts:     []Option{WithRegion("")},
			wantHost: "https://openapi.tuyaeu.com",
		},
		{
			name: "host overrides the region",
			id:   "id", key: "key",
			opts:     []Option{WithRegion(RegionChina), WithHost("http://127.0.0.1:8080")},
			wantHost: "http://127.0.0.1:8080",
		},
		{
			name: "a trailing slash on the host is trimmed",
			id:   "id", key: "key",
			opts:     []Option{WithHost("http://127.0.0.1:8080/")},
			wantHost: "http://127.0.0.1:8080",
		},
		{
			name: "missing access id",
			id:   "", key: "key",
			wantErr: "access ID and access key are required",
		},
		{
			name: "missing access key",
			id:   "id", key: "",
			wantErr: "access ID and access key are required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(tt.id, tt.key, tt.opts...)
			if !checkErr(t, err, tt.wantErr) {
				return
			}
			if c.host != tt.wantHost {
				t.Errorf("host = %q, want %q", c.host, tt.wantHost)
			}
			if c.http == nil {
				t.Error("http client is nil")
			}
		})
	}
}

func TestClientGet(t *testing.T) {
	pinClock(t)

	type result struct {
		Name string `json:"name"`
	}

	tests := []struct {
		name      string
		platform  *platform
		query     url.Values
		want      result
		wantCalls []call
		wantErr   string
	}{
		{
			name:     "signs and decodes the result",
			platform: &platform{handler: jsonHandler(`{"success":true,"result":{"name":"kitchen"}}`)},
			want:     result{Name: "kitchen"},
			wantCalls: []call{{
				method: http.MethodGet,
				path:   "/v1.0/devices/dev1",
				header: http.Header{
					"Client_id":    {"testaccessid"},
					"Sign_method":  {"HMAC-SHA256"},
					"T":            {"1787486400000"},
					"Nonce":        {testNonce},
					"Access_token": {"tok1"},
				},
			}},
		},
		{
			name:     "query parameters reach the platform",
			platform: &platform{handler: jsonHandler(`{"success":true,"result":{"name":"kitchen"}}`)},
			query:    url.Values{"page_no": {"1"}, "page_size": {"20"}},
			want:     result{Name: "kitchen"},
			wantCalls: []call{{
				method: http.MethodGet,
				path:   "/v1.0/devices/dev1",
				query:  "page_no=1&page_size=20",
			}},
		},
		{
			name:     "every value of a repeated query key is signed",
			platform: &platform{handler: jsonHandler(`{"success":true,"result":{"name":"kitchen"}}`)},
			query:    url.Values{"a": {"1", "2"}, "b": {"3"}},
			want:     result{Name: "kitchen"},
			wantCalls: []call{{
				method: http.MethodGet,
				path:   "/v1.0/devices/dev1",
				query:  "a=1&a=2&b=3",
			}},
		},
		{
			name:     "a business error surfaces as an APIError",
			platform: &platform{handler: jsonHandler(`{"success":false,"code":1000,"msg":"device not exist"}`)},
			wantErr:  "code 1000: device not exist",
		},
		{
			name: "a json error page from a proxy surfaces as an HTTPError",
			platform: &platform{handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = io.WriteString(w, `{"error":"bad gateway"}`)
			}},
			wantErr: "tuya http /v1.0/devices/dev1: 502",
		},
		{
			name: "a non-JSON response surfaces as an HTTPError",
			platform: &platform{handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = io.WriteString(w, "<html>gateway timeout</html>")
			}},
			wantErr: "gateway timeout",
		},
		{
			name: "an expired token is replaced and the call retried",
			platform: &platform{handler: func(w http.ResponseWriter, r *http.Request) {
				// The first attempt carries tok1 and is rejected; the retry
				// carries tok2 and succeeds.
				if r.Header.Get("access_token") == "tok1" {
					_, _ = io.WriteString(w, `{"success":false,"code":1010,"msg":"token invalid"}`)
					return
				}
				_, _ = io.WriteString(w, `{"success":true,"result":{"name":"kitchen"}}`)
			}},
			want: result{Name: "kitchen"},
			wantCalls: []call{
				{method: http.MethodGet, path: "/v1.0/devices/dev1", header: http.Header{"Access_token": {"tok1"}}},
				{method: http.MethodGet, path: "/v1.0/devices/dev1", header: http.Header{"Access_token": {"tok2"}}},
			},
		},
		{
			name:     "a persistent token error is reported",
			platform: &platform{handler: jsonHandler(`{"success":false,"code":1010,"msg":"token invalid"}`)},
			wantErr:  "code 1010",
		},
		{
			name:     "a token the platform refuses to issue fails the call",
			platform: &platform{tokenReplies: []string{`{"success":false,"code":1001,"msg":"sign invalid"}`}},
			wantErr:  "obtain access token",
		},
		{
			name:     "an empty token is rejected",
			platform: &platform{tokenReplies: []string{`{"success":true,"result":{"access_token":""}}`}},
			wantErr:  "returned an empty token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, p := newPlatform(t, tt.platform)

			var got result
			err := c.Get(t.Context(), "/v1.0/devices/dev1", tt.query, &got)
			if !checkErr(t, err, tt.wantErr) {
				return
			}
			if got != tt.want {
				t.Errorf("result = %+v, want %+v", got, tt.want)
			}
			assertCalls(t, p.requests(false), tt.wantCalls)
		})
	}
}

func TestClientPost(t *testing.T) {
	pinClock(t)

	tests := []struct {
		name      string
		platform  *platform
		body      any
		wantCalls []call
		wantErr   string
	}{
		{
			name:     "encodes the body as json",
			platform: &platform{handler: jsonHandler(`{"success":true,"result":true}`)},
			body:     map[string]string{"code": "switch_1"},
			wantCalls: []call{{
				method: http.MethodPost,
				path:   "/v1.0/devices/dev1/commands",
				body:   `{"code":"switch_1"}`,
				header: http.Header{"Content-Type": {"application/json"}},
			}},
		},
		{
			name:     "a nil body sends no payload at all",
			platform: &platform{handler: jsonHandler(`{"success":true,"result":true}`)},
			wantCalls: []call{{
				method: http.MethodPost,
				path:   "/v1.0/devices/dev1/commands",
			}},
		},
		{
			name:     "an unencodable body is refused before any request",
			platform: &platform{},
			body:     make(chan int),
			wantErr:  "encode request body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, p := newPlatform(t, tt.platform)

			err := c.Post(t.Context(), "/v1.0/devices/dev1/commands", tt.body, nil)
			if !checkErr(t, err, tt.wantErr) {
				return
			}
			assertCalls(t, p.requests(false), tt.wantCalls)
		})
	}
}

// TestClientTokenRefresh drives several goroutines into the same expired
// token: however the schedule falls, the platform must see exactly two token
// requests — the initial fetch and one shared replacement — and every call
// must succeed. Each extra fetch would invalidate the previous waiter's token.
func TestClientTokenRefresh(t *testing.T) {
	pinClock(t)

	c, p := newPlatform(t, &platform{handler: func(w http.ResponseWriter, r *http.Request) {
		// tok1 has been revoked; any later token works.
		if r.Header.Get("access_token") == "tok1" {
			_, _ = io.WriteString(w, `{"success":false,"code":1010,"msg":"token invalid"}`)
			return
		}
		_, _ = io.WriteString(w, `{"success":true,"result":{}}`)
	}})

	const workers = 5
	errs := make(chan error, workers)
	for range workers {
		go func() { errs <- c.Get(t.Context(), "/v1.0/devices/dev1", nil, nil) }()
	}
	for range workers {
		if err := <-errs; err != nil {
			t.Errorf("call failed: %v", err)
		}
	}

	if got := p.tokenCount(); got != 2 {
		t.Errorf("platform saw %d token requests, want 2 (initial fetch + one shared refresh)", got)
	}
}

// TestClientTransportFailure covers the paths a live server cannot produce:
// the HTTP client itself failing.
func TestClientTransportFailure(t *testing.T) {
	pinClock(t)

	tests := []struct {
		name    string
		build   func(*gomock.Controller) Doer
		wantErr string
	}{
		{
			name: "transport error while fetching a token",
			build: func(ctrl *gomock.Controller) Doer {
				d := NewMockDoer(ctrl)
				d.EXPECT().Do(gomock.Any()).Return(nil, errTransport)
				return d
			},
			wantErr: "obtain access token",
		},
		{
			name: "unreadable response body",
			build: func(ctrl *gomock.Controller) Doer {
				d := NewMockDoer(ctrl)
				d.EXPECT().Do(gomock.Any()).Return(&http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(brokenReader{}),
				}, nil)
				return d
			},
			wantErr: "read response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New("id", "key", WithHTTPClient(tt.build(gomock.NewController(t))))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			err = c.Get(t.Context(), "/v1.0/devices/dev1", nil, nil)
			checkErr(t, err, tt.wantErr)
		})
	}
}

// brokenReader fails on the first read, standing in for a connection that dies
// mid-response.
type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// assertCalls checks the recorded requests against the expected ones. Only the
// fields a case fills in are compared, so a case states just what it cares
// about; header maps are matched key by key for the same reason.
func assertCalls(t *testing.T, got, want []call) {
	t.Helper()
	if want == nil {
		return
	}
	if len(got) != len(want) {
		t.Fatalf("got %d requests, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if want[i].method != "" && got[i].method != want[i].method {
			t.Errorf("request %d method = %s, want %s", i, got[i].method, want[i].method)
		}
		if want[i].path != "" && got[i].path != want[i].path {
			t.Errorf("request %d path = %s, want %s", i, got[i].path, want[i].path)
		}
		if want[i].escaped != "" && got[i].escaped != want[i].escaped {
			t.Errorf("request %d escaped path = %s, want %s", i, got[i].escaped, want[i].escaped)
		}
		if want[i].query != "" && got[i].query != want[i].query {
			t.Errorf("request %d query = %s, want %s", i, got[i].query, want[i].query)
		}
		if got[i].body != want[i].body {
			t.Errorf("request %d body = %s, want %s", i, got[i].body, want[i].body)
		}
		for key, values := range want[i].header {
			if got := got[i].header.Get(key); got != values[0] {
				t.Errorf("request %d header %s = %q, want %q", i, key, got, values[0])
			}
		}
		if want[i].header.Get("sign") == "" && got[i].header.Get("sign") == "" {
			t.Errorf("request %d carries no signature", i)
		}
	}
}
