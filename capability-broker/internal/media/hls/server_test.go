package hls

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandler_404UnknownSession(t *testing.T) {
	dir := t.TempDir()
	h := Handler(dir, func(string) bool { return false })

	req := httptest.NewRequest(http.MethodGet, "/_hls/sess_unknown/playlist.m3u8", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d want=404", rec.Code)
	}
}

func TestHandler_ServesPlaylist(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sess_a"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	playlist := "#EXTM3U\n#EXT-X-VERSION:6\n"
	if err := os.WriteFile(filepath.Join(dir, "sess_a", "playlist.m3u8"), []byte(playlist), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	h := Handler(dir, func(id string) bool { return id == "sess_a" })

	req := httptest.NewRequest(http.MethodGet, "/_hls/sess_a/playlist.m3u8", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status=%d want=200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/vnd.apple.mpegurl" {
		t.Errorf("Content-Type=%q want=application/vnd.apple.mpegurl", got)
	}
	if !strings.Contains(rec.Body.String(), "#EXTM3U") {
		t.Errorf("body=%q want EXTM3U", rec.Body.String())
	}
}

func TestHandler_ServesFmp4Segment(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sess_b", "1080p"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sess_b", "1080p", "segment_00001.m4s"), []byte("BINARY"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h := Handler(dir, func(id string) bool { return id == "sess_b" })

	req := httptest.NewRequest(http.MethodGet, "/_hls/sess_b/1080p/segment_00001.m4s", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status=%d want=200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "video/iso.segment" {
		t.Errorf("Content-Type=%q want=video/iso.segment", got)
	}
}

func TestHandler_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	h := Handler(dir, func(string) bool { return true })

	req := httptest.NewRequest(http.MethodGet, "/_hls/../etc/passwd", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d want=404", rec.Code)
	}
}

func TestScratch_SetupAndCleanup(t *testing.T) {
	root := t.TempDir()
	s := NewScratch(root, "sess_x")
	dir, err := s.Setup([]string{"240p", "720p"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if dir != filepath.Join(root, "sess_x") {
		t.Errorf("dir=%s", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "240p")); err != nil {
		t.Errorf("240p subdir missing: %v", err)
	}
	if err := s.Cleanup(); err != nil {
		t.Errorf("Cleanup: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("scratch still present after cleanup: err=%v", err)
	}
}
