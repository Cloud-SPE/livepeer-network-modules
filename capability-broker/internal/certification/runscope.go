package certification

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Run-scoped URLs for runners that fetch their input and write their
// output (plan 0045 §7; certification-steps §4).
//
// A transcode runner takes a source URL and a destination URL — the
// video never travels in the request. So a multipart fixture cannot
// certify it, and any URL the recipe author writes into a JSON body is
// one the pool invented and no runner can fetch. What the runner needs
// is a real file at a real address, and somewhere real to write.
//
// The broker provides both, scoped to one certification run, exactly as
// the usage tap (usagetap.go) provides a callback: minted when the run
// starts, unguessable, closed when the run ends, swept if abandoned.
// The fixture URL serves a file from certification_fixtures_dir; the
// sink accepts an upload and discards it, counting the bytes so a step
// can prove the runner wrote something. Neither is a public CDN or an
// open write target, because neither outlives its run.

const (
	// FixturePathPrefix + {scope}/{ref} serves a fixture file.
	FixturePathPrefix = "/internal/v1/certification/fixture/"
	// SinkPathPrefix + {scope} — or any path under it, for a runner that
	// writes several artifacts — accepts and discards an upload.
	SinkPathPrefix = "/internal/v1/certification/sink/"

	// maxSinkBytes bounds one upload. A 2s 720p probe transcodes to well
	// under a megabyte; a runner writing more than this to a probe sink
	// is doing something other than the probe.
	maxSinkBytes = 256 << 20

	// maxScopeAge mirrors maxTapAge: a scope older than this belongs to a
	// run that died between opening and its defer.
	maxScopeAge = 30 * time.Minute
)

// runScope is one run's fixture and sink capability.
type runScope struct {
	runID     string
	openedAt  time.Time
	sinkBytes atomic.Int64
	sinkPuts  atomic.Int64
}

type runScopes struct {
	mu     sync.Mutex
	scopes map[string]*runScope
}

func newRunScopes() *runScopes { return &runScopes{scopes: map[string]*runScope{}} }

// open mints a scope id. The id is the capability: it is 128 random
// bits, appears only in URLs handed to the runner under certification,
// and stops resolving when the run ends. No separate token is needed
// for a GET that serves a public test fixture or a PUT that discards.
func (s *runScopes) open(runID string, now time.Time) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("certification: mint scope id: %w", err)
	}
	id := "certrun_" + hex.EncodeToString(b)
	s.sweep(now, maxScopeAge)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scopes[id] = &runScope{runID: runID, openedAt: now}
	return id, nil
}

// close drops a scope and returns what the sink saw.
func (s *runScopes) close(id string) (bytes, puts int64) {
	s.mu.Lock()
	sc := s.scopes[id]
	delete(s.scopes, id)
	s.mu.Unlock()
	if sc == nil {
		return 0, 0
	}
	return sc.sinkBytes.Load(), sc.sinkPuts.Load()
}

func (s *runScopes) get(id string) *runScope {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scopes[id]
}

func (s *runScopes) sweep(now time.Time, maxAge time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	dropped := 0
	for id, sc := range s.scopes {
		if now.Sub(sc.openedAt) > maxAge {
			delete(s.scopes, id)
			dropped++
		}
	}
	return dropped
}

// readFixture resolves a built-in fixture ref to its file, the same way
// the multipart path does: <dir>/<ref>.<any extension>, first match by
// name. Shared with the fixture route so what a multipart step embeds
// and what a URL-fetching runner downloads are the same bytes.
func readFixture(dir, ref string) ([]byte, string, error) {
	ref = strings.TrimSpace(ref)
	if dir == "" {
		return nil, "", fmt.Errorf("fixture_missing: no certification fixtures_dir configured for ref %q", ref)
	}
	clean := filepath.Clean("/" + ref) // forbid escaping the dir
	matches, _ := filepath.Glob(filepath.Join(dir, clean+".*"))
	if len(matches) == 0 {
		matches, _ = filepath.Glob(filepath.Join(dir, clean))
	}
	if len(matches) == 0 {
		return nil, "", fmt.Errorf("fixture_missing: %q not under %s", ref, dir)
	}
	sort.Strings(matches)
	data, err := os.ReadFile(matches[0])
	if err != nil {
		return nil, "", err
	}
	return data, contentTypeFor(matches[0]), nil
}

// contentTypeFor is the handful of types a probe fixture can be. A
// runner that cares reads the body; this is a courtesy header.
func contentTypeFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4":
		return "video/mp4"
	case ".wav":
		return "audio/wav"
	case ".mp3":
		return "audio/mpeg"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".json":
		return "application/json"
	}
	return "application/octet-stream"
}

// FixtureURL and SinkURL build the addresses handed to a runner.
func FixtureURL(baseURL, scopeID, ref string) string {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		return ""
	}
	return base + FixturePathPrefix + scopeID + "/" + strings.TrimLeft(ref, "/")
}

func SinkURL(baseURL, scopeID string) string {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		return ""
	}
	return base + SinkPathPrefix + scopeID
}

// FixtureFor serves a fixture for an open scope. False means unknown
// scope or unknown ref, which a caller must not distinguish: an open
// certification run is not something a prober gets to enumerate.
func (e *Engine) FixtureFor(scopeID, ref string) (data []byte, contentType string, ok bool) {
	if e == nil || e.scopes == nil || e.scopes.get(scopeID) == nil {
		return nil, "", false
	}
	data, ct, err := readFixture(e.opts.FixturesDir, ref)
	if err != nil {
		return nil, "", false
	}
	return data, ct, true
}

// SinkAccept records an upload against an open scope. False for an
// unknown scope, same rule as above.
func (e *Engine) SinkAccept(scopeID string, n int64) bool {
	if e == nil || e.scopes == nil {
		return false
	}
	sc := e.scopes.get(scopeID)
	if sc == nil {
		return false
	}
	sc.sinkBytes.Add(n)
	sc.sinkPuts.Add(1)
	return true
}

// MaxSinkBytes is exported for the route's body limit.
const MaxSinkBytes = maxSinkBytes

// OpenScopeForTest opens a scope on a live engine and points its
// fixtures at dir. Test-only: production scopes are opened by a run.
func OpenScopeForTest(e *Engine, fixturesDir string, now time.Time) string {
	e.opts.FixturesDir = fixturesDir
	id, err := e.scopes.open("test", now)
	if err != nil {
		panic(err)
	}
	return id
}
