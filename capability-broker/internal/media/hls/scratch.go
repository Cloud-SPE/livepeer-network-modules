// Package hls owns the LL-HLS scratch directory and HTTP serving.
//
// The encoder writes per-session playlists + segments under
// <scratch>/<session_id>/{playlist.m3u8, init.mp4, segment_NNNNN.m4s,
// <rung>/...}; the HTTP handler in server.go serves anything below
// that path on the broker's existing paid listener.
//
// Cleanup is the mode driver's responsibility: on session-end (any
// of the four termination triggers) it calls Cleanup on the
// per-session scratch.
package hls

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// Scratch represents the per-session output directory.
type Scratch struct {
	Root      string
	SessionID string
}

// NewScratch returns a Scratch handle. The parent directory must
// already exist; the per-session subdirectory is created in Setup.
func NewScratch(root, sessionID string) *Scratch {
	return &Scratch{Root: root, SessionID: sessionID}
}

// Path returns the per-session scratch directory.
func (s *Scratch) Path() string {
	return filepath.Join(s.Root, s.SessionID)
}

// Setup creates the per-session directory tree (and any rung
// subdirectories the encoder will write into). Returns the absolute
// path to the per-session directory.
func (s *Scratch) Setup(rungs []string) (string, error) {
	dir := s.Path()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("hls scratch: %w", err)
	}
	for _, r := range rungs {
		if r == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Join(dir, r), 0o755); err != nil {
			return "", fmt.Errorf("hls scratch %s: %w", r, err)
		}
	}
	return dir, nil
}

// Cleanup removes the per-session directory. RemoveAll failure is a
// soft fail; we log + bubble up the error so the caller can record
// it in the cleanup-failed metric.
func (s *Scratch) Cleanup() error {
	if s == nil || s.SessionID == "" {
		return nil
	}
	dir := s.Path()
	if err := os.RemoveAll(dir); err != nil {
		log.Printf("hls scratch cleanup failed dir=%s err=%v", dir, err)
		return err
	}
	return nil
}
