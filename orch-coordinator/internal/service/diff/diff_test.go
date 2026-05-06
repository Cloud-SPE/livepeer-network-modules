package diff

import (
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

func tup(capID, off, mode, price, worker string, extra map[string]any) types.CapabilityTuple {
	return types.CapabilityTuple{
		CapabilityID:    capID,
		OfferingID:      off,
		InteractionMode: mode,
		WorkUnit:        types.WorkUnit{Name: "tokens"},
		PricePerUnitWei: price,
		WorkerURL:       worker,
		Extra:           extra,
	}
}

func TestCompute_BothEmpty(t *testing.T) {
	r, err := Compute(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 0 {
		t.Fatalf("expected 0 rows")
	}
}

func TestCompute_AddedRemoved(t *testing.T) {
	cand := &types.ManifestPayload{Capabilities: []types.CapabilityTuple{
		tup("a", "1", "http-stream@v1", "100", "https://x", nil),
	}}
	pub := &types.ManifestPayload{Capabilities: []types.CapabilityTuple{
		tup("b", "1", "http-stream@v1", "200", "https://y", nil),
	}}
	r, err := Compute(cand, pub)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(r.Rows))
	}
	got := map[string]string{}
	for _, row := range r.Rows {
		got[row.CapabilityID] = row.Drift
	}
	if got["a"] != DriftAdded {
		t.Fatalf("a: %s", got["a"])
	}
	if got["b"] != DriftRemoved {
		t.Fatalf("b: %s", got["b"])
	}
}

func TestCompute_PriceChanged(t *testing.T) {
	c := tup("a", "1", "http-stream@v1", "100", "https://x", nil)
	p := tup("a", "1", "http-stream@v1", "200", "https://x", nil)
	r, err := Compute(&types.ManifestPayload{Capabilities: []types.CapabilityTuple{c}},
		&types.ManifestPayload{Capabilities: []types.CapabilityTuple{p}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 1 || r.Rows[0].Drift != DriftPriceChanged {
		t.Fatalf("got %+v", r.Rows)
	}
}

func TestCompute_ExtraChanged(t *testing.T) {
	c := tup("a", "1", "http-stream@v1", "100", "https://x", map[string]any{"region": "us-west-2"})
	p := tup("a", "1", "http-stream@v1", "100", "https://x", map[string]any{"region": "us-east-1"})
	// Distinct keys → one row for each (added + removed), not extra_changed.
	r, err := Compute(&types.ManifestPayload{Capabilities: []types.CapabilityTuple{c}},
		&types.ManifestPayload{Capabilities: []types.CapabilityTuple{p}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 2 {
		t.Fatalf("expected 2 distinct rows by uniqueness key, got %d", len(r.Rows))
	}
}

func TestCompute_WorkerChanged(t *testing.T) {
	c := tup("a", "1", "http-stream@v1", "100", "https://b.example", nil)
	p := tup("a", "1", "http-stream@v1", "100", "https://a.example", nil)
	r, err := Compute(&types.ManifestPayload{Capabilities: []types.CapabilityTuple{c}},
		&types.ManifestPayload{Capabilities: []types.CapabilityTuple{p}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 1 || r.Rows[0].Drift != DriftWorkerChanged {
		t.Fatalf("got %+v", r.Rows)
	}
}

func TestCompute_NoChange(t *testing.T) {
	c := tup("a", "1", "http-stream@v1", "100", "https://x", nil)
	p := tup("a", "1", "http-stream@v1", "100", "https://x", nil)
	r, err := Compute(&types.ManifestPayload{Capabilities: []types.CapabilityTuple{c}},
		&types.ManifestPayload{Capabilities: []types.CapabilityTuple{p}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Counts[DriftNone] != 1 {
		t.Fatalf("expected one no-change row, got counts=%v", r.Counts)
	}
}
