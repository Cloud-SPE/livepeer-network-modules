package backend

import (
	"net/http"
	"strings"
)

// livepeerHeaderPrefix is matched case-insensitively per RFC 7230.
const livepeerHeaderPrefix = "livepeer-"

// StripLivepeerHeaders returns a clone of h with all Livepeer-* headers
// removed. The broker MUST call this before forwarding to the backend so the
// backend never sees Livepeer protocol headers.
func StripLivepeerHeaders(h http.Header) http.Header {
	out := h.Clone()
	for k := range out {
		if strings.HasPrefix(strings.ToLower(k), livepeerHeaderPrefix) {
			out.Del(k)
		}
	}
	return out
}
