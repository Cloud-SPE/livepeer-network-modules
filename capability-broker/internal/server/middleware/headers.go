package middleware

import (
	"net/http"
	"regexp"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
)

var protocolTagRE = regexp.MustCompile(`^[a-z][a-z0-9-]*/v[0-9]+$`)

// Headers validates the required Livepeer-* request headers per
// livepeer-network-protocol/headers/livepeer-headers.md (v1):
// Capability, Offering, Payment, Protocol, and Request-Id.
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
			livepeerheader.Payment,
			livepeerheader.RequestID,
		} {
			if r.Header.Get(h) == "" {
				livepeerheader.WriteBadRequest(w, "missing required header: "+h)
				return
			}
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
