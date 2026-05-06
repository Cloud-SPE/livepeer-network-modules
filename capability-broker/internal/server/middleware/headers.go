package middleware

import (
	"net/http"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
)

// Headers validates the five required Livepeer-* request headers per
// livepeer-network-protocol/headers/livepeer-headers.md.
//
// Missing headers → 400 with a descriptive message body (the spec does not
// enumerate a Livepeer-Error code for missing-header cases).
// Spec-Version major mismatch → 505 + Livepeer-Error: spec_version_unsupported.
// Mode malformed → 505 + Livepeer-Error: mode_unsupported.
//
// Cross-checks between header values and the Livepeer-Payment envelope happen
// in the Payment middleware (envelope decoding is its responsibility).
func Headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(livepeerheader.Capability) == "" {
			livepeerheader.WriteBadRequest(w, "missing required header: "+livepeerheader.Capability)
			return
		}
		if r.Header.Get(livepeerheader.Offering) == "" {
			livepeerheader.WriteBadRequest(w, "missing required header: "+livepeerheader.Offering)
			return
		}
		if r.Header.Get(livepeerheader.Payment) == "" {
			livepeerheader.WriteBadRequest(w, "missing required header: "+livepeerheader.Payment)
			return
		}
		sv := r.Header.Get(livepeerheader.SpecVersion)
		if sv == "" {
			livepeerheader.WriteBadRequest(w, "missing required header: "+livepeerheader.SpecVersion)
			return
		}
		if !majorVersionMatches(sv, livepeerheader.ImplementedSpecVersion) {
			livepeerheader.WriteError(w, http.StatusHTTPVersionNotSupported,
				livepeerheader.ErrSpecVersionUnsupported,
				"spec version "+sv+" is not supported by this broker (implemented: "+livepeerheader.ImplementedSpecVersion+")")
			return
		}
		mode := r.Header.Get(livepeerheader.Mode)
		if mode == "" {
			livepeerheader.WriteBadRequest(w, "missing required header: "+livepeerheader.Mode)
			return
		}
		if !strings.Contains(mode, "@v") {
			livepeerheader.WriteError(w, http.StatusHTTPVersionNotSupported,
				livepeerheader.ErrModeUnsupported,
				"Livepeer-Mode must be of the form '<name>@v<major>'; got "+mode)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// majorVersionMatches returns true if the major component of clientVersion
// matches that of supportedVersion. Both must be in dotted form.
func majorVersionMatches(clientVersion, supportedVersion string) bool {
	cMajor, _, _ := strings.Cut(clientVersion, ".")
	sMajor, _, _ := strings.Cut(supportedVersion, ".")
	return cMajor != "" && cMajor == sMajor
}
