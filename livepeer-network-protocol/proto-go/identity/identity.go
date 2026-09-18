// Package identity defines canonical public settlement IDs and route bindings.
package identity

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func ValidDomain(id string) bool {
	if len(id) != 66 || id[:2] != "0x" {
		return false
	}
	nonzero := false
	for _, c := range id[2:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
		nonzero = nonzero || c != '0'
	}
	return nonzero
}

// BrokerURI canonicalizes an HTTP(S) base URI without making a network request.
// ASCII DNS names (including already encoded IDNA A-labels) are required. Path
// escapes, dot segments, repeated slashes, userinfo, query and fragment are
// rejected rather than assigning ambiguous identities to them.
func BrokerURI(raw string) (string, error) {
	if strings.TrimSpace(raw) != raw {
		return "", fmt.Errorf("broker URI has surrounding whitespace")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || strings.Contains(raw, "%") {
		return "", fmt.Errorf("broker URI must be an HTTP(S) base without credentials, escapes, query or fragment")
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) == nil {
		if len(host) > 253 || strings.HasSuffix(host, ".") {
			return "", fmt.Errorf("invalid broker hostname")
		}
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", fmt.Errorf("invalid broker hostname label")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return "", fmt.Errorf("broker hostname must be ASCII; encode IDNA to A-labels first")
				}
			}
		}
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid broker port")
		}
		port = strconv.Itoa(n)
	}
	if u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80" {
		port = ""
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", fmt.Errorf("empty broker port")
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	u.Host = host
	if strings.Contains(u.Path, "//") || strings.Contains(u.Path, "\\") {
		return "", fmt.Errorf("ambiguous broker base path")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return "", fmt.Errorf("dot segments are forbidden in broker base paths")
		}
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String(), nil
}

func SameBrokerURI(a, b string) bool {
	ca, ea := BrokerURI(a)
	cb, eb := BrokerURI(b)
	return ea == nil && eb == nil && ca == cb
}
