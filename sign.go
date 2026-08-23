package tuyacloud

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// signature builds the value of the "sign" header.
//
// Tuya signs a canonical description of the request rather than the request
// itself:
//
//	stringToSign = METHOD \n SHA256(body) \n <signed headers> \n path[?sorted query]
//	sign         = HMAC-SHA256(accessID + token + t + nonce + stringToSign, key)
//
// The token is empty for the token endpoint itself and present for every other
// call, which is the only difference between the two documented schemes. No
// headers are signed, so that section is always empty — but its newline still
// counts, hence the blank line in the middle.
func signature(accessID, accessKey, token, timestamp, nonce, method string, u *url.URL, body []byte) string {
	stringToSign := strings.Join([]string{
		method,
		hashHex(body),
		"", // no signed headers
		canonicalURL(u),
	}, "\n")
	return strings.ToUpper(hmacHex(accessKey, accessID+token+timestamp+nonce+stringToSign))
}

// canonicalURL renders the path with its query parameters sorted by key, which
// is the form the signature is computed over. Values are used as they were
// decoded, not re-escaped.
func canonicalURL(u *url.URL) string {
	// The escaped form is what travels on the wire, and therefore what the
	// platform verifies the signature against. For the plain alphanumeric IDs
	// Tuya issues the two forms are identical, but signing the decoded path
	// would silently break on any ID carrying a reserved character.
	path := u.EscapedPath()
	if u.RawQuery == "" {
		return path
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(values) == 0 {
		return path
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(path)
	first := true
	for _, k := range keys {
		// Every value of a repeated key is signed, in the order it travels on
		// the wire; taking just the first would silently break the signature.
		for _, v := range values[k] {
			if first {
				b.WriteByte('?')
				first = false
			} else {
				b.WriteByte('&')
			}
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(v)
		}
	}
	return b.String()
}

// hashHex is the lower-case hex SHA-256 of the request body. An empty body
// hashes to the well-known digest of the empty string rather than to nothing.
func hashHex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func hmacHex(key, data string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}

// randomNonce returns the per-request nonce that, combined with the timestamp,
// makes a captured signature useless for a replay.
func randomNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on any supported platform; if it somehow
		// does, a timestamp still varies per request, unlike a constant.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}
