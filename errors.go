package tuyacloud

import "fmt"

// APIError is a business error reported by Tuya: the HTTP request itself
// succeeded, but the platform refused it. Match it with errors.As to inspect
// the code, for example to tell an expired token from a missing device.
type APIError struct {
	Code int    // Tuya error code, e.g. 1010 for an invalid token
	Msg  string // human-readable message from the platform
	Path string // request path that produced it
}

// Error implements the error interface.
func (e *APIError) Error() string {
	return fmt.Sprintf("tuya api %s: code %d: %s", e.Path, e.Code, e.Msg)
}

// HTTPError reports a response the client could not interpret as a Tuya
// envelope, which usually means a proxy, an outage or a wrong host.
type HTTPError struct {
	StatusCode int
	Status     string
	Path       string
	Body       string // truncated response body, for diagnosis
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("tuya http %s: %s: %s", e.Path, e.Status, e.Body)
}
