package tuyacloud

import (
	"net/url"
	"testing"
)

// The expected signatures below were produced by an independent transcription
// of Tuya's documented algorithm (in Python), so a match confirms this package
// agrees with the specification rather than merely with itself.
func TestSignature(t *testing.T) {
	const (
		accessID  = "testaccessid"
		accessKey = "testsecret"
		timestamp = "1700000000000"
		nonce     = "abcdef0123456789abcdef0123456789"
	)

	tests := []struct {
		name   string
		token  string
		method string
		rawURL string
		body   []byte
		want   string
	}{
		{
			name:   "token request signs without an access token",
			method: "GET",
			rawURL: "https://openapi.tuyaeu.com/v1.0/token?grant_type=1",
			want:   "4820B8271F43BCBC743F8274F600C5DE120BE2E33F16309C18D9BA0472E1E1EC",
		},
		{
			name:   "business request includes the access token",
			token:  "tok123",
			method: "GET",
			rawURL: "https://openapi.tuyaeu.com/v1.0/devices/dev1/status",
			want:   "7B2566E5E0C07EBEBCB245202B11386CCA5D812E2A1B3D7E9EF618121581FA3C",
		},
		{
			name:   "post signs the body digest",
			token:  "tok123",
			method: "POST",
			rawURL: "https://openapi.tuyaeu.com/v1.0/devices/dev1/commands",
			body:   []byte(`{"commands":[{"code":"switch_1","value":true}]}`),
			want:   "331F3B1D72F0CFB6438CDCF9CCFC1DB5BAC642F872C99E4711E77BE8973B254D",
		},
		{
			name:   "query parameters are sorted by key",
			token:  "tok123",
			method: "GET",
			rawURL: "https://openapi.tuyaeu.com/v1.0/devices?z=3&a=1&b=2",
			want:   "173822AE1CD840909B91BCC675C3CDEEC0337C946131A17607A9E20D145741AF",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}
			got := signature(accessID, accessKey, tt.token, timestamp, nonce, tt.method, u, tt.body)
			if got != tt.want {
				t.Errorf("signature = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestCanonicalURL(t *testing.T) {
	tests := []struct {
		name   string
		rawURL string
		want   string
	}{
		{
			name:   "no query is just the path",
			rawURL: "https://host/v1.0/devices/dev1",
			want:   "/v1.0/devices/dev1",
		},
		{
			name:   "keys are sorted",
			rawURL: "https://host/v1.0/devices?page_size=20&device_ids=a&page_no=1",
			want:   "/v1.0/devices?device_ids=a&page_no=1&page_size=20",
		},
		{
			name:   "values are signed decoded, not re-escaped",
			rawURL: "https://host/v1.0/devices?name=living%20room",
			want:   "/v1.0/devices?name=living room",
		},
		{
			name:   "repeated keys keep every value in wire order",
			rawURL: "https://host/v1.0/devices?a=1&b=3&a=2",
			want:   "/v1.0/devices?a=1&a=2&b=3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}
			if got := canonicalURL(u); got != tt.want {
				t.Errorf("canonicalURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRandomNonce(t *testing.T) {
	first, second := randomNonce(), randomNonce()
	if len(first) != 32 {
		t.Errorf("nonce length = %d, want 32", len(first))
	}
	if first == second {
		t.Error("two nonces are identical, so they are not random")
	}
}
