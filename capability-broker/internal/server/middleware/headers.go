package middleware

import (
	"encoding/json"
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
		// Capability and Offering — required, non-empty.
		if r.Header.Get(livepeerheader.Capability) == "" {
			writeMissingHeader(w, livepeerheader.Capability)
			return
		}
		if r.Header.Get(livepeerheader.Offering) == "" {
			writeMissingHeader(w, livepeerheader.Offering)
			return
		}
		// Payment — required.
		if r.Header.Get(livepeerheader.Payment) == "" {
			writeMissingHeader(w, livepeerheader.Payment)
			return
		}
		// Spec-Version — required + major must match.
		sv := r.Header.Get(livepeerheader.SpecVersion)
		if sv == "" {
			writeMissingHeader(w, livepeerheader.SpecVersion)
			return
		}
		if !majorVersionMatches(sv, livepeerheader.ImplementedSpecVersion) {
			writeError(w, http.StatusHTTPVersionNotSupported,
				livepeerheader.ErrSpecVersionUnsupported,
				"spec version "+sv+" is not supported by this broker (implemented: "+livepeerheader.ImplementedSpecVersion+")")
			return
		}
		// Mode — required + format <name>@v<major>.
		mode := r.Header.Get(livepeerheader.Mode)
		if mode == "" {
			writeMissingHeader(w, livepeerheader.Mode)
			return
		}
		if !strings.Contains(mode, "@v") {
			writeError(w, http.StatusHTTPVersionNotSupported,
				livepeerheader.ErrModeUnsupported,
				"Livepeer-Mode must be of the form '<name>@v<major>'; got "+mode)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// majorVersionMatches returns true if the major component of clientVersion
// matches that of supportedVersion. Both must be in dotted form (X, X.Y, or
// X.Y.Z).
func majorVersionMatches(clientVersion, supportedVersion string) bool {
	cMajor, _, _ := strings.Cut(clientVersion, ".")
	sMajor, _, _ := strings.Cut(supportedVersion, ".")
	return cMajor != "" && cMajor == sMajor
}

func writeMissingHeader(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "missing required header: " + name,
	})
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set(livepeerheader.Error, code)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   code,
		"message": msg,
	})
}
