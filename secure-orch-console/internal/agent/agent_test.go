package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/audit"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/lastsigned"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/signing"
)

const testToken = "agent-secret"

// fakeCoordinator is the §11 end-to-end harness counterpart: candidate
// route with ETag/304, signed-manifest receive that "publishes", and
// the public well-known route.
type fakeCoordinator struct {
	mu          sync.Mutex
	manifest    []byte
	etag        string
	published   []byte
	rejectPush  bool
	pulls       int
	notModified int
	pushes      int
	requests    int
}

func (f *fakeCoordinator) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /candidate.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests++
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		if f.manifest == nil {
			http.Error(w, "no candidate", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("ETag", f.etag)
		if r.Header.Get("If-None-Match") == f.etag {
			f.notModified++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		f.pulls++
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)
		for name, body := range map[string][]byte{"manifest.json": f.manifest, "metadata.json": []byte(`{"note":"test"}`)} {
			_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body))})
			_, _ = tw.Write(body)
		}
		_ = tw.Close()
		_ = gw.Close()
		_, _ = w.Write(buf.Bytes())
	})
	mux.HandleFunc("POST /admin/signed-manifest", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests++
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		if f.rejectPush {
			http.Error(w, "rejected", http.StatusConflict)
			return
		}
		body := make([]byte, 0)
		buf := bytes.NewBuffer(body)
		_, _ = buf.ReadFrom(r.Body)
		f.published = buf.Bytes()
		f.pushes++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"outcome":"accepted"}`))
	})
	mux.HandleFunc("GET /.well-known/livepeer-registry.json", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests++
		if f.published == nil {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(f.published)
	})
	return mux
}

func (f *fakeCoordinator) setCandidate(etag string, manifest []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.etag, f.manifest = etag, manifest
}

func (f *fakeCoordinator) counts() (pulls, notModified, pushes, requests int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pulls, f.notModified, f.pushes, f.requests
}

func (f *fakeCoordinator) publishedBytes() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.published
}

type harness struct {
	agent  *Agent
	coord  *fakeCoordinator
	dir    string
	now    time.Time
	alerts []string
	t      *testing.T
}

func (h *harness) advance(d time.Duration) { h.now = h.now.Add(d) }

func (h *harness) writePolicy(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.dir, "sign-policy.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func policyJSON(renewal, benign bool, stabilitySeconds, maxPerHour int) string {
	return fmt.Sprintf(`{
  "policy_version": 1,
  "auto_sign": {"renewal": %v, "benign": %v},
  "benign_bounds": {
    "price_delta_max_pct": 10,
    "allow_tuple_removal": true,
    "worker_url_domain_allowlist": ["workers.example-orch.net"]
  },
  "rate_limit": {"max_auto_signs_per_hour": %d, "on_breach": "pause"},
  "stability_window_seconds": %d,
  "renewal_threshold_fraction": 0.3333
}`, renewal, benign, maxPerHour, stabilitySeconds)
}

func newHarness(t *testing.T, policyBody string) *harness {
	t.Helper()
	coord := &fakeCoordinator{}
	srv := httptest.NewServer(coord.handler())
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	h := &harness{coord: coord, dir: dir, now: time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC), t: t}

	if err := os.WriteFile(filepath.Join(dir, "sign-policy.json"), []byte(policyBody), 0o600); err != nil {
		t.Fatal(err)
	}
	signer, err := signing.FromHexKey("0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d")
	if err != nil {
		t.Fatal(err)
	}
	log, err := audit.Open(filepath.Join(dir, "audit.jsonl"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })

	client := &Client{AdminURL: srv.URL, PublicURL: srv.URL, Token: testToken, HTTP: srv.Client()}
	ag := New(Config{
		PolicyPath:      filepath.Join(dir, "sign-policy.json"),
		LastSignedPath:  filepath.Join(dir, "last-signed.json"),
		HeldDir:         filepath.Join(dir, "held"),
		PauseFile:       filepath.Join(dir, "agent.pause"),
		PushMaxAttempts: 2,
	}, client, signer, log, slog.Default(), func(kind string, _ map[string]any) {
		h.alerts = append(h.alerts, kind)
	})
	ag.now = func() time.Time { return h.now }
	ag.sleep = func(context.Context, time.Duration) {}
	h.agent = ag
	return h
}

func (h *harness) auditKinds() []string {
	h.t.Helper()
	events, err := audit.ReadRecent(filepath.Join(h.dir, "audit.jsonl"), 100)
	if err != nil {
		h.t.Fatal(err)
	}
	kinds := make([]string, 0, len(events))
	for _, ev := range events {
		kinds = append(kinds, string(ev.Kind))
	}
	return kinds
}

func countKind(kinds []string, want string) int {
	n := 0
	for _, k := range kinds {
		if k == want {
			n++
		}
	}
	return n
}

func testManifest(t *testing.T, seq uint64, issued, expires time.Time, price string, extraTuple bool) []byte {
	t.Helper()
	caps := []any{map[string]any{
		"capability_id":      "openai:chat-completions",
		"offering_id":        "vllm-h100",
		"protocol":           "paid-job/v1",
		"job":                map[string]any{"transports": []any{"unary", "stream"}},
		"work_unit":          map[string]any{"name": "tokens"},
		"price_per_unit_wei": price,
		"worker_url":         "https://a.workers.example-orch.net/",
	}}
	if extraTuple {
		caps = append(caps, map[string]any{
			"capability_id":      "video:transcode",
			"offering_id":        "default",
			"protocol":           "paid-session/v1",
			"session":            map[string]any{"descriptor_schema": "rtmp-hls/v1", "metering": "runner-reported"},
			"work_unit":          map[string]any{"name": "minutes"},
			"price_per_unit_wei": "5",
			"worker_url":         "https://b.example/",
		})
	}
	body, err := json.Marshal(map[string]any{
		"spec_version":    "0.1.0",
		"publication_seq": seq,
		"issued_at":       issued.UTC().Format(time.RFC3339Nano),
		"expires_at":      expires.UTC().Format(time.RFC3339Nano),
		"orch":            map[string]any{"eth_address": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		"capabilities":    caps,
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func (h *harness) seedLastSigned(t *testing.T, manifest []byte) {
	t.Helper()
	envelope, err := json.Marshal(map[string]any{
		"manifest":  json.RawMessage(manifest),
		"signature": map[string]any{"algorithm": "secp256k1", "value": "0xdead", "canonicalization": "JCS"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := lastsigned.WriteAtomic(filepath.Join(h.dir, "last-signed.json"), envelope); err != nil {
		t.Fatal(err)
	}
	// The seeded envelope counts as already published so reconcile
	// doesn't push it before the scenario starts.
	h.coord.mu.Lock()
	h.coord.published = envelope
	h.coord.mu.Unlock()
}

func TestAgent_AutoSignsRenewalWithSeqDiscipline(t *testing.T) {
	h := newHarness(t, policyJSON(true, false, 0, 4))
	// Last-signed: 24h TTL, 4h remaining — inside the 8h renewal window.
	h.seedLastSigned(t, testManifest(t, 5, h.now.Add(-20*time.Hour), h.now.Add(4*time.Hour), "1000", false))
	// Candidate: identical content, fresh window, coordinator seq lags.
	h.coord.setCandidate(`"etag-1"`, testManifest(t, 5, h.now, h.now.Add(24*time.Hour), "1000", false))

	h.agent.Cycle(context.Background())

	_, _, pushes, _ := h.coord.counts()
	if pushes != 1 {
		t.Fatalf("pushes=%d want 1", pushes)
	}
	seq, _, err := envelopeSeqAndHash(h.coord.publishedBytes())
	if err != nil {
		t.Fatal(err)
	}
	if seq != 6 {
		t.Fatalf("published seq=%d want 6 (max(candidate 5, last-signed 5+1))", seq)
	}
	onDisk, err := lastsigned.Load(filepath.Join(h.dir, "last-signed.json"))
	if err != nil {
		t.Fatal(err)
	}
	diskSeq, diskSHA, err := envelopeSeqAndHash(onDisk)
	if err != nil {
		t.Fatal(err)
	}
	pubSeq, pubSHA, _ := envelopeSeqAndHash(h.coord.publishedBytes())
	if diskSeq != pubSeq || diskSHA != pubSHA {
		t.Fatal("last-signed on disk must match what was published")
	}
	kinds := h.auditKinds()
	for _, want := range []string{"candidate_pulled", "classified", "auto_sign", "push_attempt", "publish_confirmed", "policy_loaded"} {
		if countKind(kinds, want) == 0 {
			t.Fatalf("missing audit kind %s in %v", want, kinds)
		}
	}
}

func TestAgent_NoOpWhenValidityRemains(t *testing.T) {
	h := newHarness(t, policyJSON(true, false, 0, 4))
	h.seedLastSigned(t, testManifest(t, 5, h.now.Add(-4*time.Hour), h.now.Add(20*time.Hour), "1000", false))
	h.coord.setCandidate(`"etag-1"`, testManifest(t, 6, h.now, h.now.Add(24*time.Hour), "1000", false))

	h.agent.Cycle(context.Background())

	_, _, pushes, _ := h.coord.counts()
	if pushes != 0 {
		t.Fatalf("pushes=%d want 0", pushes)
	}
	if countKind(h.auditKinds(), "no_op") != 1 {
		t.Fatalf("expected one no_op audit event: %v", h.auditKinds())
	}
}

func TestAgent_HoldsCriticalAndSupersedes(t *testing.T) {
	h := newHarness(t, policyJSON(true, false, 0, 4))
	h.seedLastSigned(t, testManifest(t, 5, h.now.Add(-4*time.Hour), h.now.Add(20*time.Hour), "1000", false))
	// New tuple → critical.
	h.coord.setCandidate(`"etag-1"`, testManifest(t, 6, h.now, h.now.Add(24*time.Hour), "1000", true))

	h.agent.Cycle(context.Background())

	item, candBytes, err := h.agent.Held().Current()
	if err != nil {
		t.Fatal(err)
	}
	if item == nil || item.Class != "critical" || item.ETag != `"etag-1"` {
		t.Fatalf("held item: %+v", item)
	}
	if len(candBytes) == 0 {
		t.Fatal("held candidate bytes missing")
	}
	if countKind(h.auditKinds(), "held") != 1 {
		t.Fatalf("expected held audit: %v", h.auditKinds())
	}

	// A newer critical candidate replaces the held slot.
	h.advance(time.Minute)
	h.coord.setCandidate(`"etag-2"`, testManifest(t, 7, h.now, h.now.Add(24*time.Hour), "9999", true))
	h.agent.Cycle(context.Background())

	item, _, err = h.agent.Held().Current()
	if err != nil {
		t.Fatal(err)
	}
	if item.ETag != `"etag-2"` {
		t.Fatalf("held etag=%s want etag-2", item.ETag)
	}
	kinds := h.auditKinds()
	if countKind(kinds, "held_superseded") != 1 || countKind(kinds, "held") != 2 {
		t.Fatalf("supersede audit trail wrong: %v", kinds)
	}
	_, _, pushes, _ := h.coord.counts()
	if pushes != 0 {
		t.Fatal("held candidates must not push")
	}
}

func TestAgent_BenignHeldWithShadowAudit(t *testing.T) {
	h := newHarness(t, policyJSON(true, false, 0, 4))
	h.seedLastSigned(t, testManifest(t, 5, h.now.Add(-4*time.Hour), h.now.Add(20*time.Hour), "1000", false))
	// Price within ±10% → benign; phase-1 dial holds it.
	h.coord.setCandidate(`"etag-1"`, testManifest(t, 6, h.now, h.now.Add(24*time.Hour), "1050", false))

	h.agent.Cycle(context.Background())

	kinds := h.auditKinds()
	if countKind(kinds, "would_auto_sign") != 1 || countKind(kinds, "held") != 1 {
		t.Fatalf("shadow audit trail wrong: %v", kinds)
	}
	item, _, _ := h.agent.Held().Current()
	if item == nil || !item.ShadowAutoSign {
		t.Fatalf("held item must carry would_auto_sign: %+v", item)
	}

	// Phase-2 dial: same change auto-signs.
	h.writePolicy(t, policyJSON(true, true, 0, 4))
	h.advance(time.Minute)
	h.coord.setCandidate(`"etag-2"`, testManifest(t, 6, h.now, h.now.Add(24*time.Hour), "1080", false))
	h.agent.Cycle(context.Background())
	_, _, pushes, _ := h.coord.counts()
	if pushes != 1 {
		t.Fatalf("phase-2 benign should auto-sign: pushes=%d", pushes)
	}
}

func TestAgent_RefusesForbidden(t *testing.T) {
	h := newHarness(t, policyJSON(true, true, 0, 4))
	h.seedLastSigned(t, testManifest(t, 5, h.now.Add(-4*time.Hour), h.now.Add(20*time.Hour), "1000", false))
	cand := testManifest(t, 6, h.now, h.now.Add(24*time.Hour), "1000", false)
	cand = bytes.Replace(cand, []byte("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), []byte("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), 1)
	h.coord.setCandidate(`"etag-1"`, cand)

	h.agent.Cycle(context.Background())

	if countKind(h.auditKinds(), "refused") != 1 {
		t.Fatalf("expected refused audit: %v", h.auditKinds())
	}
	item, _, _ := h.agent.Held().Current()
	if item != nil {
		t.Fatal("forbidden candidate must never enter the held queue")
	}
	_, _, pushes, _ := h.coord.counts()
	if pushes != 0 {
		t.Fatal("forbidden candidate must not push")
	}
	if countKind(h.alerts, "forbidden_candidate") != 1 {
		t.Fatalf("alerts=%v", h.alerts)
	}
}

func TestAgent_StabilityWindowDebounces(t *testing.T) {
	h := newHarness(t, policyJSON(true, false, 300, 4))
	h.seedLastSigned(t, testManifest(t, 5, h.now.Add(-20*time.Hour), h.now.Add(4*time.Hour), "1000", false))
	h.coord.setCandidate(`"etag-1"`, testManifest(t, 5, h.now, h.now.Add(24*time.Hour), "1000", false))

	// Pull happens; window not yet elapsed → no action.
	h.agent.Cycle(context.Background())
	if _, _, pushes, _ := h.coord.counts(); pushes != 0 {
		t.Fatal("acted before stability window elapsed")
	}

	// A newer ETag inside the window resets the clock.
	h.advance(200 * time.Second)
	h.coord.setCandidate(`"etag-2"`, testManifest(t, 5, h.now, h.now.Add(24*time.Hour), "1000", false))
	h.agent.Cycle(context.Background())
	h.advance(200 * time.Second) // 400s after first pull, 200s after reset
	h.agent.Cycle(context.Background())
	if _, _, pushes, _ := h.coord.counts(); pushes != 0 {
		t.Fatal("reset window not honored")
	}

	h.advance(150 * time.Second) // 350s after the reset
	h.agent.Cycle(context.Background())
	if _, _, pushes, _ := h.coord.counts(); pushes != 1 {
		t.Fatal("stable candidate should have been signed after the window")
	}
}

func TestAgent_CrashRecoveryResumesPush(t *testing.T) {
	h := newHarness(t, policyJSON(true, false, 0, 4))
	// last-signed exists on disk; nothing published (crash happened
	// after sign, before push). No candidate on the coordinator.
	envelope, err := json.Marshal(map[string]any{
		"manifest":  json.RawMessage(testManifest(t, 7, h.now.Add(-time.Hour), h.now.Add(23*time.Hour), "1000", false)),
		"signature": map[string]any{"algorithm": "secp256k1", "value": "0xdead", "canonicalization": "JCS"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := lastsigned.WriteAtomic(filepath.Join(h.dir, "last-signed.json"), envelope); err != nil {
		t.Fatal(err)
	}

	h.agent.Cycle(context.Background())

	_, _, pushes, _ := h.coord.counts()
	if pushes != 1 {
		t.Fatalf("pushes=%d want 1 (resume)", pushes)
	}
	pubSeq, _, err := envelopeSeqAndHash(h.coord.publishedBytes())
	if err != nil {
		t.Fatal(err)
	}
	if pubSeq != 7 {
		t.Fatalf("published seq=%d want 7", pubSeq)
	}
	if countKind(h.auditKinds(), "publish_confirmed") != 1 {
		t.Fatalf("expected publish_confirmed: %v", h.auditKinds())
	}
}

func TestAgent_PushFailureAuditsAndAlerts(t *testing.T) {
	h := newHarness(t, policyJSON(true, false, 0, 4))
	h.coord.rejectPush = true
	envelope, _ := json.Marshal(map[string]any{
		"manifest":  json.RawMessage(testManifest(t, 7, h.now.Add(-time.Hour), h.now.Add(23*time.Hour), "1000", false)),
		"signature": map[string]any{"algorithm": "secp256k1", "value": "0xdead", "canonicalization": "JCS"},
	})
	if err := lastsigned.WriteAtomic(filepath.Join(h.dir, "last-signed.json"), envelope); err != nil {
		t.Fatal(err)
	}

	h.agent.Cycle(context.Background())

	kinds := h.auditKinds()
	if countKind(kinds, "push_attempt") != 2 {
		t.Fatalf("push attempts: %v", kinds)
	}
	if countKind(kinds, "publish_failed") != 1 {
		t.Fatalf("expected publish_failed: %v", kinds)
	}
	if countKind(h.alerts, "publish_failed") != 1 {
		t.Fatalf("alerts=%v", h.alerts)
	}
}

func TestAgent_PauseFileStopsPullAndSign(t *testing.T) {
	h := newHarness(t, policyJSON(true, false, 0, 4))
	h.coord.setCandidate(`"etag-1"`, testManifest(t, 5, h.now, h.now.Add(24*time.Hour), "1000", false))
	if err := os.WriteFile(filepath.Join(h.dir, "agent.pause"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	h.agent.Cycle(context.Background())
	h.agent.Cycle(context.Background())

	if _, _, _, requests := h.coord.counts(); requests != 0 {
		t.Fatalf("paused agent made %d requests", requests)
	}
	kinds := h.auditKinds()
	if countKind(kinds, "agent_paused") != 1 {
		t.Fatalf("pause transition should audit once: %v", kinds)
	}

	if err := os.Remove(filepath.Join(h.dir, "agent.pause")); err != nil {
		t.Fatal(err)
	}
	h.agent.Cycle(context.Background())
	if countKind(h.auditKinds(), "agent_resumed") != 1 {
		t.Fatalf("expected agent_resumed: %v", h.auditKinds())
	}
}

func TestAgent_InvalidPolicyFailsClosed(t *testing.T) {
	h := newHarness(t, policyJSON(true, false, 0, 4))
	h.coord.setCandidate(`"etag-1"`, testManifest(t, 5, h.now, h.now.Add(24*time.Hour), "1000", false))
	h.writePolicy(t, `{"policy_version": 1, "tpyo": true}`)

	h.agent.Cycle(context.Background())
	h.agent.Cycle(context.Background())

	if _, _, _, requests := h.coord.counts(); requests != 0 {
		t.Fatalf("agent with invalid policy made %d requests", requests)
	}
	kinds := h.auditKinds()
	if countKind(kinds, "policy_invalid") != 1 {
		t.Fatalf("policy_invalid should audit once per transition: %v", kinds)
	}
	if countKind(h.alerts, "policy_invalid") != 1 {
		t.Fatalf("alerts=%v", h.alerts)
	}
}

func TestAgent_RateLimitLatchHoldsInsteadOfSigning(t *testing.T) {
	h := newHarness(t, policyJSON(true, false, 0, 1))
	h.seedLastSigned(t, testManifest(t, 5, h.now.Add(-20*time.Hour), h.now.Add(4*time.Hour), "1000", false))
	h.coord.setCandidate(`"etag-1"`, testManifest(t, 5, h.now, h.now.Add(24*time.Hour), "1000", false))

	// First renewal consumes the budget of 1.
	h.agent.Cycle(context.Background())
	if _, _, pushes, _ := h.coord.counts(); pushes != 1 {
		t.Fatalf("first renewal should sign: pushes=%d", pushes)
	}

	// Force another due renewal inside the same hour.
	h.advance(10 * time.Minute)
	h.seedLastSigned(t, testManifest(t, 6, h.now.Add(-20*time.Hour), h.now.Add(4*time.Hour), "1000", false))
	h.coord.setCandidate(`"etag-2"`, testManifest(t, 6, h.now, h.now.Add(24*time.Hour), "1000", false))
	h.agent.Cycle(context.Background())

	if _, _, pushes, _ := h.coord.counts(); pushes != 1 {
		t.Fatal("rate-limited renewal must not sign")
	}
	kinds := h.auditKinds()
	if countKind(kinds, "rate_limit_pause") != 1 {
		t.Fatalf("expected rate_limit_pause: %v", kinds)
	}
	item, _, _ := h.agent.Held().Current()
	if item == nil || item.Class != "renewal" {
		t.Fatalf("rate-limited candidate should be held: %+v", item)
	}
	if countKind(h.alerts, "rate_limit_pause") != 1 {
		t.Fatalf("alerts=%v", h.alerts)
	}
}

func TestClient_RejectsUnexpectedTarMember(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, body := range map[string][]byte{"manifest.json": []byte(`{"spec_version":"0.1.0"}`), "sneaky.sh": []byte("#!/bin/sh")} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body))})
		_, _ = tw.Write(body)
	}
	_ = tw.Close()
	_ = gw.Close()
	if _, err := parseCandidateTarball(buf.Bytes()); err == nil {
		t.Fatal("unexpected tar member must be rejected")
	}
}
