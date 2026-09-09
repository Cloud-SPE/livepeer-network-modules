package middleware

import (
	"net/http"
	"regexp"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
)

var protocolTagRE = regexp.MustCompile(`^[a-z][a-z0-9-]*/v[0-9]+$`)

// Headers validates the required Livepeer-* request headers per
// livepeer-network-protocol/headers/livepeer-headers.md (v1):
// Capability, Offering, Protocol, Request-Id, and exactly one payment mode:
// legacy Livepeer-Payment or account-backed Livepeer-Authorization (with an
// optional Livepeer-Payment top-up).
//
// Missing headers → 400 with a descriptive message body.
// Protocol malformed → 505 + Livepeer-Error: protocol_unsupported.
// Whether the named protocol is implemented for the capability is the
// route handler's decision; cross-checks between header values and the
// payment envelope happen in the Payment middleware.
func Headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range []string{
			livepeerheader.Capability,
			livepeerheader.Offering,
			livepeerheader.RequestID,
		} {
			if r.Header.Get(h) == "" {
				livepeerheader.WriteBadRequest(w, "missing required header: "+h)
				return
			}
		}
		if r.Header.Get(livepeerheader.Payment) == "" && r.Header.Get(livepeerheader.Authorization) == "" {
			livepeerheader.WriteBadRequest(w, "missing required payment mode: "+livepeerheader.Payment+" or "+livepeerheader.Authorization)
			return
		}
		proto := r.Header.Get(livepeerheader.Protocol)
		if proto == "" {
			livepeerheader.WriteBadRequest(w, "missing required header: "+livepeerheader.Protocol)
			return
		}
		if !protocolTagRE.MatchString(proto) {
			livepeerheader.WriteError(w, http.StatusHTTPVersionNotSupported,
				livepeerheader.ErrProtocolUnsupported,
				"Livepeer-Protocol must be of the form '<name>/v<major>'; got "+proto)
			return
		}
		next.ServeHTTP(w, r)
	})
}
