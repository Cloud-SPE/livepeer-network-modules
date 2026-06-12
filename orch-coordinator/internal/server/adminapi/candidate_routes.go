package adminapi

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/repo/candidates"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/service/candidate"
)

// candidateRetryAfterSeconds is the Retry-After hint on the 503
// returned before the first candidate build (plan 0042 §5.1).
const candidateRetryAfterSeconds = "30"

// CandidateRoutes wires the per-candidate admin endpoints onto the
// Server's mux. Two endpoints land in commit 2:
//
//	GET /candidate.json      — JCS-canonical manifest.json bytes
//	GET /candidate.tar.gz    — packed tarball (manifest + sidecar)
//
// commits 3+ append /diff, /roster, /admin/signed-manifest, and the
// web UI routes against the same mux.
//
// Both routes carry an ETag over the candidate's full canonical
// manifest bytes and honor If-None-Match with 304 (plan 0042 §5.1),
// so the secure-orch agent can poll at ~zero cost. The full-bytes
// hash — not the builder's content-only hash — is deliberate: it
// stays stable across no-op rebuilds (the builder debounces
// issued_at) but moves when a renewal window or seq advance produces
// a candidate the agent must see.
func (s *Server) CandidateRoutes(builder *candidate.Builder, store *candidates.Store, auditLog *audit.Log) {
	s.mux.HandleFunc("GET /candidate.json", s.requireAuthOrAgent(func(w http.ResponseWriter, r *http.Request) {
		setCandidateHeaders(w)
		body, ok := latestManifestBytes(w, builder, store)
		if !ok {
			return
		}
		if writeCandidateETag(w, r, body) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))

	s.mux.HandleFunc("GET /candidate.tar.gz", s.requireAuthOrAgent(func(w http.ResponseWriter, r *http.Request) {
		setCandidateHeaders(w)
		manifest, ok := latestManifestBytes(w, builder, store)
		if !ok {
			return
		}
		if writeCandidateETag(w, r, manifest) {
			return
		}
		body, err := store.LatestTarball()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				w.Header().Set("Retry-After", candidateRetryAfterSeconds)
				http.Error(w, "no candidate built yet", http.StatusServiceUnavailable)
				return
			}
			http.Error(w, fmt.Sprintf("read tarball: %s", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", "attachment; filename=\"candidate.tar.gz\"")
		if auditLog != nil {
			if cand := builder.Latest(); cand != nil {
				if _, appendErr := auditLog.Append(audit.Event{
					Outcome:        audit.OutcomeCandidateDownloaded,
					Actor:          actorFromRequest(r),
					Uploader:       actorFromRequest(r),
					ManifestSHA256: candidate.SHA256Hex(cand.ManifestBytes),
					PublicationSeq: cand.Manifest.PublicationSeq,
					Note:           "candidate tarball downloaded",
				}); appendErr != nil {
					s.logger.Warn("audit append failed", "err", appendErr)
				}
			}
		}
		_, _ = w.Write(body)
	}))
}

// latestManifestBytes resolves the latest candidate's canonical
// manifest bytes, preferring the in-memory builder and falling back
// to the on-disk store (coordinator restarted before the first
// rebuild). On failure it writes the error response and returns
// ok=false.
func latestManifestBytes(w http.ResponseWriter, builder *candidate.Builder, store *candidates.Store) ([]byte, bool) {
	if cand := builder.Latest(); cand != nil {
		return cand.ManifestBytes, true
	}
	body, err := store.LatestManifest()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			w.Header().Set("Retry-After", candidateRetryAfterSeconds)
			http.Error(w, "no candidate built yet", http.StatusServiceUnavailable)
			return nil, false
		}
		http.Error(w, fmt.Sprintf("read latest: %s", err), http.StatusInternalServerError)
		return nil, false
	}
	return body, true
}

// writeCandidateETag sets the ETag header and, when If-None-Match
// matches, writes 304 and reports true (caller must not write a
// body). 304s deliberately precede the download audit append — polls
// are metrics, not audit events (plan 0042 §9).
func writeCandidateETag(w http.ResponseWriter, r *http.Request, manifestBytes []byte) bool {
	etag := `"` + candidate.SHA256Hex(manifestBytes) + `"`
	w.Header().Set("ETag", etag)
	if ifNoneMatchHit(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

// ifNoneMatchHit reports whether the If-None-Match header value
// matches the current ETag. Weak validators (W/ prefix) compare by
// the opaque tag, per RFC 9110 §13.1.2's weak comparison for
// If-None-Match.
func ifNoneMatchHit(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "W/")
		if part == etag {
			return true
		}
	}
	return false
}

func setCandidateHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
}
