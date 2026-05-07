package hls

import (
	"net/http"
	"path/filepath"
	"strings"
)

// SessionExists reports whether a given session_id is currently open.
// The HTTP handler calls into this to 404 unknown sessions before
// touching the filesystem.
type SessionExists func(sessionID string) bool

// Handler returns an http.Handler that serves files from
// <scratch>/<session_id>/<rest> on the broker's paid listener at
// /_hls/<session_id>/<rest>.
//
// 404s when the session is unknown; the URL itself is treated as a
// per-session bearer secret per the spec, so playback isn't gated by
// the payment middleware (covered at session-open).
func Handler(scratchRoot string, exists SessionExists) http.Handler {
	const prefix = "/_hls/"
	fs := http.Dir(scratchRoot)
	server := http.FileServer(fs)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		idx := strings.IndexByte(rest, '/')
		if idx <= 0 {
			http.NotFound(w, r)
			return
		}
		sessionID := rest[:idx]
		if exists != nil && !exists(sessionID) {
			http.NotFound(w, r)
			return
		}
		clean := filepath.Clean(rest)
		if strings.HasPrefix(clean, "..") {
			http.NotFound(w, r)
			return
		}
		switch {
		case strings.HasSuffix(rest, ".m3u8"):
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Header().Set("Cache-Control", "no-store")
		case strings.HasSuffix(rest, ".m4s"), strings.HasSuffix(rest, ".mp4"):
			w.Header().Set("Content-Type", "video/iso.segment")
		case strings.HasSuffix(rest, ".ts"):
			w.Header().Set("Content-Type", "video/MP2T")
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + clean
		server.ServeHTTP(w, r2)
	})
}
