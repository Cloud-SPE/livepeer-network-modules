package certification

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
)

// fixturesDir ships one probe file, the way an operator's
// certification_fixtures_dir does.
func fixturesDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "video"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "video", "mp4-2s-720p.mp4"), []byte("not really mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// scopedExec is a run with a scope open, as run() would have it.
func scopedExec(t *testing.T, base string) (*runExec, *Engine) {
	t.Helper()
	e := New(nil, Options{FixturesDir: fixturesDir(t), CallbackBaseURL: base})
	t.Cleanup(e.Close)
	x := &runExec{engine: e, ctx: context.Background(), runID: "run-1", fixturesDir: e.opts.FixturesDir,
		cap: &runnerattach.Capability{}, offer: config.Offer{OfferingID: "vod"}}
	if base != "" {
		id, err := e.scopes.open(x.runID, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		x.scopeID = id
	}
	return x, e
}

// Both tokens resolve to URLs under the broker's base, scoped to the run.
func TestFixtureAndSinkTokensSubstituteToRunScopedURLs(t *testing.T) {
	x, _ := scopedExec(t, "https://broker.example/")
	got, err := x.substituteConfig(map[string]any{
		"body": map[string]any{"source_url": "{{fixture_url.video/mp4-2s-720p}}", "output_url": "{{sink_url}}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := got["body"].(map[string]any)
	src, sink := body["source_url"].(string), body["output_url"].(string)
	if !strings.HasPrefix(src, "https://broker.example"+FixturePathPrefix+x.scopeID+"/video/mp4-2s-720p") {
		t.Fatalf("source_url = %q", src)
	}
	if sink != "https://broker.example"+SinkPathPrefix+x.scopeID {
		t.Fatalf("output_url = %q", sink)
	}
	// And the engine serves exactly that fixture for exactly that scope.
	data, ct, ok := x.engine.FixtureFor(x.scopeID, "video/mp4-2s-720p")
	if !ok || string(data) != "not really mp4" || ct != "video/mp4" {
		t.Fatalf("FixtureFor = %q %q %v", data, ct, ok)
	}
}

// A recipe naming a fixture the broker does not have fails at
// substitution, naming the token — not later as a 404 the runner
// reports as its own failure.
func TestUnknownFixtureRefFailsAtSubstitution(t *testing.T) {
	x, _ := scopedExec(t, "https://broker.example")
	_, err := x.substituteConfig(map[string]any{"u": "{{fixture_url.video/does-not-exist}}"})
	if err == nil || !strings.Contains(err.Error(), "substitution_missing") || !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("err = %v", err)
	}
}

// Without external_base_url the broker has no address to hand out, and
// that is the operator's gap, not the recipe's or the runner's.
func TestRunScopedTokensNameTheMissingBaseURL(t *testing.T) {
	x, _ := scopedExec(t, "")
	for _, tok := range []string{"{{sink_url}}", "{{fixture_url.video/mp4-2s-720p}}"} {
		_, err := x.substituteConfig(map[string]any{"u": tok})
		if err == nil || !strings.Contains(err.Error(), "external_base_url") {
			t.Fatalf("%s: err = %v, want it to name external_base_url", tok, err)
		}
	}
}

// The scope is the run's: opened before the first step, closed when the
// run ends, whatever the run did.
func TestRunOpensAndClosesItsScope(t *testing.T) {
	conn := &handlerConn{h: http.NotFoundHandler()}
	e := New(testRegistryWith(t, conn, jobCapability()),
		Options{Extractors: extractorRegistry(), CallbackBaseURL: "https://broker.example"})
	t.Cleanup(e.Close)
	// A recipe whose only step needs the sink URL: substitution succeeds
	// only if the scope was open BEFORE the step ran.
	x := &runExec{engine: e, ctx: context.Background(), conn: conn, runID: "run-2", cap: jobCapability(),
		offer: config.Offer{OfferingID: "o", Certification: []config.CertificationStep{{
			Name: "probe", Type: "request", Config: map[string]any{"transport": "unary", "body": map[string]any{"u": "{{sink_url}}"}},
		}}}}
	steps, _, _ := x.run()
	if len(steps) != 1 {
		t.Fatalf("steps = %d", len(steps))
	}
	// The runner 404s, so the step fails — but it fails on the exchange,
	// not on substitution, which is what proves the scope was there.
	if steps[0].Status == StepError && strings.Contains(steps[0].Message, "substitution_missing") {
		t.Fatalf("scope was not open when the step ran: %s", steps[0].Message)
	}
	if x.scopeID != "" {
		t.Fatal("scope survived the run's defer")
	}
	e.scopes.mu.Lock()
	open := len(e.scopes.scopes)
	e.scopes.mu.Unlock()
	if open != 0 {
		t.Fatalf("%d scope(s) still open after the run ended", open)
	}
}

func TestUnknownScopeIsRefusedUniformly(t *testing.T) {
	_, e := scopedExec(t, "https://broker.example")
	if _, _, ok := e.FixtureFor("certrun_nope", "video/mp4-2s-720p"); ok {
		t.Fatal("a fixture was served for an unknown scope")
	}
	if e.SinkAccept("certrun_nope", 10) {
		t.Fatal("an upload was accepted for an unknown scope")
	}
}

func TestSinkCountsWhatItDiscards(t *testing.T) {
	x, e := scopedExec(t, "https://broker.example")
	if !e.SinkAccept(x.scopeID, 100) || !e.SinkAccept(x.scopeID, 23) {
		t.Fatal("uploads refused for an open scope")
	}
	bytes, puts := e.scopes.close(x.scopeID)
	if bytes != 123 || puts != 2 {
		t.Fatalf("close() = %d bytes / %d puts, want 123 / 2", bytes, puts)
	}
	if e.SinkAccept(x.scopeID, 1) {
		t.Fatal("a closed scope still accepted an upload")
	}
}

func TestSweepDropsAbandonedScopes(t *testing.T) {
	s := newRunScopes()
	start := time.Now().UTC()
	if _, err := s.open("run-x", start); err != nil {
		t.Fatal(err)
	}
	if n := s.sweep(start.Add(time.Minute), maxScopeAge); n != 0 {
		t.Fatalf("swept %d young scopes", n)
	}
	if n := s.sweep(start.Add(maxScopeAge+time.Minute), maxScopeAge); n != 1 {
		t.Fatalf("swept %d abandoned scopes, want 1", n)
	}
}

// A ref must not walk out of the fixtures directory. The scope id is
// unguessable, but the ref is authored by whoever wrote the recipe.
func TestReadFixtureRefusesPathEscape(t *testing.T) {
	dir := fixturesDir(t)
	if err := os.WriteFile(filepath.Join(dir, "..", "secret.txt"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readFixture(dir, "../secret"); err == nil {
		t.Fatal("a ref escaped the fixtures directory")
	}
	if data, _, err := readFixture(dir, "video/mp4-2s-720p"); err != nil || string(data) != "not really mp4" {
		t.Fatalf("a legitimate ref failed: %v", err)
	}
}
