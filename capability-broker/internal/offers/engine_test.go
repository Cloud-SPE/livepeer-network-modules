package offers

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runners"
)

type fakeConn struct{}

func (fakeConn) Close() error { return nil }
func (fakeConn) Forward(context.Context, backend.ForwardRequest) (*http.Response, error) {
	return nil, nil
}

func testDoc(host, model string) []byte {
	m := map[string]any{
		"contract_version": "1.0",
		"credential":       map[string]any{"kind": "bearer", "token": "lpc_x"},
		"host_id":          host,
		"agent_version":    "t/1",
		"hardware":         []any{},
		"capabilities": []any{map[string]any{
			"capability_id": "openai:chat-completions", "protocol": "paid-job/v1", "local_id": "chat",
			"transports":      []any{"unary"},
			"work_unit":       map[string]any{"name": "tokens", "extractor": map[string]any{"type": "openai-usage"}},
			"paths":           map[string]any{"invoke": "/v1/x"},
			"readiness":       map[string]any{"type": "http-status"},
			"identity":        map[string]any{"openai.model": model},
			"schema_versions": map[string]any{"paid-job/v1": "1.0.15"},
			"x-quant":         "fp8",
		}},
	}
	b, _ := json.Marshal(m)
	return b
}

func known() runnerattach.Known {
	return runnerattach.Known{
		Extractor:  func(n string) bool { return n == "openai-usage" },
		ProbeTypes: map[string]bool{"http-status": true},
		Protocols:  map[string]bool{"paid-job/v1": true, "paid-session/v1": true},
		Credential: func(_, _ string) (string, bool, bool) { return "", true, true },
	}
}

func attach(t *testing.T, reg *runners.Registry, host, model string) {
	t.Helper()
	doc, res := runnerattach.Evaluate(testDoc(host, model), known())
	if doc == nil {
		t.Fatalf("attach rejected: %+v", res)
	}
	reg.Attach("conn-"+host, fakeConn{}, runners.Enrollment{}, doc, res)
}

func offerCfg(match string) *config.Config {
	return &config.Config{
		OffersSource: config.OffersSourceFile,
		Offers: []config.Offer{{
			OfferingID: "shared", Capability: "openai:chat-completions", Protocol: "paid-job/v1",
			Match:           map[string]string{"identity.openai.model": match},
			Price:           config.Price{AmountWei: "1", PerUnits: 1},
			ExtraFromRunner: []string{"x-quant"},
		}},
	}
}

func newEngine(t *testing.T, reg *runners.Registry, cfg *config.Config) *Engine {
	t.Helper()
	e, err := New(cfg, reg, filepath.Join(t.TempDir(), "offers-state.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	reg.OnChange = func(host string) { e.Rematch(host) }
	return e
}

func TestFreezeOnFirstCertifiedAndMatchNeverAdopt(t *testing.T) {
	reg := runners.New(0)
	e := newEngine(t, reg, offerCfg("llama"))

	// No runner: unfrozen, not advertised.
	v, _ := e.ViewOf("shared")
	if v.State != OfferUnfrozen || v.Advertised || len(e.AdvertisedOffers()) != 0 {
		t.Fatalf("pre-attach view: %+v", v)
	}

	// First matching runner (empty certification => certify on match) freezes.
	attach(t, reg, "h1", "llama")
	v, _ = e.ViewOf("shared")
	if v.State != OfferFrozen || !v.Advertised || v.Frozen == nil || v.Frozen.FrozenBy.HostID != "h1" {
		t.Fatalf("post-freeze view: %+v", v)
	}
	if v.Runners.Eligible != 1 {
		t.Fatalf("counts: %+v", v.Runners)
	}
	frozenHash := v.Frozen.ShapeHash
	if v.Frozen.Projection.Promoted["x-quant"] == nil {
		t.Fatalf("promoted key missing: %+v", v.Frozen.Projection)
	}

	// A non-matching runner never matches.
	attach(t, reg, "h2", "mistral")
	v, _ = e.ViewOf("shared")
	if v.Runners.Eligible != 1 || v.Runners.Ineligible != 0 {
		t.Fatalf("non-matching runner counted: %+v", v.Runners)
	}

	// Same-shape second runner: eligible; the freeze does not move.
	attach(t, reg, "h3", "llama")
	v, _ = e.ViewOf("shared")
	if v.Runners.Eligible != 2 || v.Frozen.ShapeHash != frozenHash || v.Frozen.FrozenBy.HostID != "h1" {
		t.Fatalf("second runner: %+v", v)
	}
	if len(e.EligiblePairs("shared")) != 2 {
		t.Fatalf("eligible pairs: %v", e.EligiblePairs("shared"))
	}

	// A runner whose promoted x-* differs is certified but INELIGIBLE, a
	// candidate — and the offer is unchanged.
	doc, res := runnerattach.Evaluate(testDoc("h4", "llama"), known())
	raw, _ := json.Marshal("int8")
	doc.Capabilities[0].Extensions["x-quant"] = raw
	reg.Attach("conn-h4", fakeConn{}, runners.Enrollment{}, doc, res)
	v, _ = e.ViewOf("shared")
	if v.Runners.Ineligible != 1 || v.Frozen.ShapeHash != frozenHash || len(v.Candidates) != 1 {
		t.Fatalf("mismatching runner: %+v cands=%d", v.Runners, len(v.Candidates))
	}
	cand := v.Candidates[0]
	if len(cand.Diff) == 0 || cand.Diff[0].Field != "/promoted/x-quant" {
		t.Fatalf("candidate diff: %+v", cand.Diff)
	}
	pv := e.PairsFor("h4", "chat")
	if len(pv) != 1 || pv[0].State != PairIneligible || pv[0].Reason == nil || pv[0].Reason.Field != "/promoted/x-quant" {
		t.Fatalf("pair view: %+v", pv)
	}

	// accept-shape: wrong hash refused; the candidate flips eligibility.
	if _, _, err := e.AcceptShape("shared", "sha256:nope"); err != ErrNotCandidate {
		t.Fatalf("bad hash: %v", err)
	}
	elig, inelig, err := e.AcceptShape("shared", cand.ShapeHash)
	if err != nil || elig != 1 || inelig != 2 {
		t.Fatalf("accept-shape: %v elig=%d inelig=%d", err, elig, inelig)
	}
	v, _ = e.ViewOf("shared")
	if v.State != OfferSuperseding || v.Pending == nil || v.Frozen.ShapeHash != frozenHash {
		t.Fatalf("superseding view: %+v", v)
	}
	// Advertised carries the ACCEPTED shape; dispatch still the published one.
	adv := e.AdvertisedOffers()
	if len(adv) != 1 || adv[0].Shape.ShapeHash != cand.ShapeHash {
		t.Fatalf("advertised: %+v", adv)
	}
	if pairs := e.EligiblePairs("shared"); len(pairs) != 2 {
		t.Fatalf("dispatch pairs during supersede: %v", pairs)
	}
	// Confirm publish: the pending shape becomes the served one.
	if err := e.ConfirmPublished("shared", cand.ShapeHash); err != nil {
		t.Fatal(err)
	}
	v, _ = e.ViewOf("shared")
	if v.State != OfferFrozen || v.Frozen.ShapeHash != cand.ShapeHash || v.Pending != nil {
		t.Fatalf("after confirm: %+v", v)
	}
	if pairs := e.EligiblePairs("shared"); len(pairs) != 1 || pairs[0].HostID != "h4" {
		t.Fatalf("dispatch pairs after confirm: %v", pairs)
	}
}

func TestFreezeSurvivesRestartAndReload(t *testing.T) {
	reg := runners.New(0)
	dir := t.TempDir()
	path := filepath.Join(dir, "offers-state.json")
	cfg := offerCfg("llama")
	e, err := New(cfg, reg, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg.OnChange = func(host string) { e.Rematch(host) }
	attach(t, reg, "h1", "llama")
	v, _ := e.ViewOf("shared")
	hash := v.Frozen.ShapeHash

	// Restart: a new engine over the same path serves the same freeze
	// with no runner attached.
	reg2 := runners.New(0)
	e2, err := New(cfg, reg2, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	v2, _ := e2.ViewOf("shared")
	if v2.State != OfferFrozen || v2.Frozen.ShapeHash != hash || !v2.Advertised {
		t.Fatalf("after restart: %+v", v2)
	}

	// Reload with a price change keeps the freeze; dropping the offer
	// deletes it.
	cfg2 := offerCfg("llama")
	cfg2.Offers[0].Price.AmountWei = "2"
	if err := e2.Reload(cfg2); err != nil {
		t.Fatal(err)
	}
	v2, _ = e2.ViewOf("shared")
	if v2.Frozen == nil || v2.Frozen.ShapeHash != hash || v2.Operator.Price.AmountWei != "2" {
		t.Fatalf("after price reload: %+v", v2)
	}
	if err := e2.Reload(&config.Config{OffersSource: config.OffersSourceFile}); err != nil {
		t.Fatal(err)
	}
	if _, ok := e2.ViewOf("shared"); ok {
		t.Fatal("dropped offer still present")
	}
	e3, err := New(cfg, runners.New(0), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v3, _ := e3.ViewOf("shared"); v3.Frozen != nil {
		t.Fatalf("freeze survived offer deletion: %+v", v3)
	}
}

func TestAdminPushKeepsFreezeAndIsIdempotent(t *testing.T) {
	reg := runners.New(0)
	cfg := &config.Config{OffersSource: config.OffersSourceAdmin}
	e, err := New(cfg, reg, filepath.Join(t.TempDir(), "s.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	reg.OnChange = func(host string) { e.Rematch(host) }
	offers := offerCfg("llama").Offers
	changed, err := e.Push("r1", offers)
	if err != nil || len(changed) != 1 {
		t.Fatalf("push: %v %v", err, changed)
	}
	attach(t, reg, "h1", "llama")
	v, _ := e.ViewOf("shared")
	hash := v.Frozen.ShapeHash
	// Idempotent re-push: no changes, freeze intact.
	changed, err = e.Push("r1", offers)
	if err != nil || len(changed) != 0 {
		t.Fatalf("re-push: %v %v", err, changed)
	}
	// Price change: operator field moves, freeze stays.
	offers2 := offerCfg("llama").Offers
	offers2[0].Price.AmountWei = "9"
	if changed, err = e.Push("r2", offers2); err != nil || len(changed) != 1 {
		t.Fatalf("price push: %v %v", err, changed)
	}
	v, _ = e.ViewOf("shared")
	if v.Frozen.ShapeHash != hash || v.Operator.Price.AmountWei != "9" || e.Revision() != "r2" {
		t.Fatalf("after price push: %+v rev=%s", v, e.Revision())
	}
	// File-sourced engine refuses Push.
	ef, _ := New(offerCfg("x"), reg, "", nil)
	if _, err := ef.Push("r", offers); err != ErrSourceIsFile {
		t.Fatalf("file push: %v", err)
	}
}

func TestFailedCertificationStaysMatched(t *testing.T) {
	reg := runners.New(0)
	cfg := offerCfg("llama")
	cfg.Offers[0].Certification = []config.CertificationStep{{Name: "ready", Type: "readiness"}}
	e := newEngine(t, reg, cfg) // default certifier: non-empty steps => pending
	attach(t, reg, "h1", "llama")
	v, _ := e.ViewOf("shared")
	if v.State != OfferUnfrozen || v.Runners.Matched != 1 {
		t.Fatalf("pending cert: %+v", v)
	}
	// The certification engine reports failure: still matched, reason kept.
	key := PairKey{HostID: "h1", LocalID: "chat", OfferingID: "shared"}
	e.RecordCertification(key, CertOutcome{Passed: false, State: "failed", RunID: "run-1",
		Reason: &runnerattach.Reason{Code: "certification_failed", Message: "smoke 500"}})
	v, _ = e.ViewOf("shared")
	if v.State != OfferUnfrozen || v.Runners.Matched != 1 {
		t.Fatalf("failed cert: %+v", v)
	}
	pv := e.PairsFor("h1", "chat")
	if pv[0].Certification == nil || pv[0].Certification.State != "failed" {
		t.Fatalf("pair cert: %+v", pv[0])
	}
	// Then a pass freezes.
	e.RecordCertification(key, CertOutcome{Passed: true, State: "passed", RunID: "run-2"})
	v, _ = e.ViewOf("shared")
	if v.State != OfferFrozen || v.Frozen.FrozenBy.RunID != "run-2" || v.Runners.Eligible != 1 {
		t.Fatalf("passed cert: %+v", v)
	}
}
