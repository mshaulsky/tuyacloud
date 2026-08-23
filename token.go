package tuyacloud

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// token is the payload of the token endpoint.
type token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpireTime   int64  `json:"expire_time"` // lifetime in seconds, not a deadline
	UID          string `json:"uid"`
}

// Error codes the platform returns often enough to be worth matching. Compare
// them against APIError.Code with errors.As.
const (
	// CodeDataNotExist means the object — usually a device ID — is unknown.
	CodeDataNotExist = 1000
	// CodeSecretInvalid means the access ID or access key was rejected, which
	// on otherwise correct credentials usually means the wrong data centre.
	CodeSecretInvalid = 1001
	// CodeTokenExpired means the access token is no longer usable. The client
	// handles it transparently by fetching a new token and retrying once.
	CodeTokenExpired = 1010
)

// tokenPath is the simple-mode token endpoint. Simple mode (grant_type=1) is
// what a cloud project with a linked app account uses.
const tokenPath = "/v1.0/token"

// accessToken returns a cached token, fetching one if none is valid.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && now().Before(c.tokenExp) {
		return c.token, nil
	}
	return c.fetchToken(ctx)
}

// refreshToken replaces a token the platform has rejected. Concurrent callers
// burnt by the same stale token reuse the first replacement instead of each
// invalidating the last: without that, every waiter would fetch its own token.
func (c *Client) refreshToken(ctx context.Context, stale string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && c.token != stale && now().Before(c.tokenExp) {
		return c.token, nil
	}
	c.token, c.tokenExp = "", time.Time{}
	return c.fetchToken(ctx)
}

// fetchToken requests a token and caches it. The caller must hold c.mu.
//
// A new token is requested outright rather than exchanged through the refresh
// endpoint: in simple mode the credentials alone are enough, and this avoids a
// second expiry to track.
func (c *Client) fetchToken(ctx context.Context) (string, error) {
	var t token
	query := url.Values{"grant_type": []string{"1"}}
	if err := c.call(ctx, http.MethodGet, tokenPath, query, nil, "", &t); err != nil {
		return "", fmt.Errorf("tuyacloud: obtain access token: %w", err)
	}
	if t.AccessToken == "" {
		return "", fmt.Errorf("tuyacloud: obtain access token: the platform returned an empty token")
	}

	c.token = t.AccessToken
	c.tokenExp = now().Add(time.Duration(t.ExpireTime) * time.Second).Add(-tokenLeeway)
	c.uid = t.UID
	return c.token, nil
}

// isTokenError reports whether an API error code means the token needs
// replacing rather than the request being wrong.
func isTokenError(code int) bool {
	return code == CodeTokenExpired
}
