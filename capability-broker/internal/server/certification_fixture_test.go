package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/certification"
)

// fixtureServer is a broker with one probe fixture on disk and, via the
// engine's internals, one certification run holding a scope open.
func fixtureServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := newOffersTestServer(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "video"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "video", "mp4-2s-720p.mp4"), []byte("probe bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	scope := certification.OpenScopeForTest(srv.certEngine, dir, time.Now().UTC())
	ts := httptest.NewServer(srv.mux)
	t.Cleanup(ts.Close)
	return ts, scope
}

func TestCertificationFixtureIsServedForALiveScopeOnly(t *testing.T) {
	ts, scope := fixtureServer(t)

	resp, err := http.Get(ts.URL + certification.FixturePathPrefix + scope + "/video/mp4-2s-720p")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "probe bytes" {
		t.Fatalf("live scope: %d %q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("content-type = %q", ct)
	}

	// Unknown scope and unknown ref are the same answer: a prober must
	// not learn which runs are open from the difference.
	for _, path := range []string{
		certification.FixturePathPrefix + "certrun_nope/video/mp4-2s-720p",
		certification.FixturePathPrefix + scope + "/video/does-not-exist",
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestCertificationSinkAcceptsDiscardsAndCounts(t *testing.T) {
	ts, scope := fixtureServer(t)

	put := func(scope string, body []byte) (int, string) {
		req, _ := http.NewRequest(http.MethodPut, ts.URL+certification.SinkPathPrefix+scope, bytes.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		out, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, string(out)
	}
	if code, out := put(scope, []byte("transcoded output")); code != http.StatusOK || !strings.Contains(out, `"bytes":17`) {
		t.Fatalf("live scope: %d %s", code, out)
	}
	if code, _ := put("certrun_nope", []byte("x")); code != http.StatusNotFound {
		t.Fatalf("unknown scope: %d, want 404", code)
	}
	// Over the cap is refused even for a live scope; the sink is for a
	// probe's output, not for storage.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+certification.SinkPathPrefix+scope,
		io.LimitReader(zeroReader{}, certification.MaxSinkBytes+1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("over cap: %d, want 413", resp.StatusCode)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
