package adminapi

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/candidates"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/candidate"
)

// CandidateRoutes wires the per-candidate admin endpoints onto the
// Server's mux. Two endpoints land in commit 2:
//
//	GET /candidate.json      — JCS-canonical manifest.json bytes
//	GET /candidate.tar.gz    — packed tarball (manifest + sidecar)
//
// commits 3+ append /diff, /roster, /admin/signed-manifest, and the
// web UI routes against the same mux.
func (s *Server) CandidateRoutes(builder *candidate.Builder, store *candidates.Store) {
	s.mux.HandleFunc("GET /candidate.json", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		setCandidateHeaders(w)
		if cand := builder.Latest(); cand != nil {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(cand.ManifestBytes)
			return
		}
		body, err := store.LatestManifest()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.Error(w, "no candidate built yet", http.StatusServiceUnavailable)
				return
			}
			http.Error(w, fmt.Sprintf("read latest: %s", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))

	s.mux.HandleFunc("GET /candidate.tar.gz", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		setCandidateHeaders(w)
		body, err := store.LatestTarball()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.Error(w, "no candidate built yet", http.StatusServiceUnavailable)
				return
			}
			http.Error(w, fmt.Sprintf("read tarball: %s", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", "attachment; filename=\"candidate.tar.gz\"")
		_, _ = w.Write(body)
	}))
}

func setCandidateHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
}
