package utils

import (
	"net/url"
	"strings"
)

// IsHTTPSURL reports whether s parses as a valid http(s) URL with a
// host. http://localhost:* is permitted because dev-mode operators
// frequently run against a local manifest host.
func IsHTTPSURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return strings.HasPrefix(u.Host, "localhost") || strings.HasPrefix(u.Host, "127.0.0.1")
	default:
		return false
	}
}
