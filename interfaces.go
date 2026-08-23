package tuyacloud

import "net/http"

//go:generate go tool mockgen -source=interfaces.go -destination=mocks_test.go -package=tuyacloud

// Doer performs HTTP requests. *http.Client satisfies it, and so does any
// wrapper adding retries, tracing or rate limiting.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}
