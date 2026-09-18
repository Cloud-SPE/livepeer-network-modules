package diff

import (
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/types"
)

// A broker advertises per_units: 1 explicitly; the signed manifest
// omits it (absent means 1). Same price, no drift.
func TestCompute_PerUnitsAbsentEqualsOne(t *testing.T) {
	advertised := tup("video:transcode.live", "gateway-ingest", "paid-session/v1", "1000000000000", "https://eu", nil)
	advertised.PerUnits = 1
	published := tup("video:transcode.live", "gateway-ingest", "paid-session/v1", "1000000000000", "https://eu", nil)
	published.PerUnits = 0

	r, err := Compute(
		&types.ManifestPayload{Capabilities: []types.CapabilityTuple{advertised}},
		&types.ManifestPayload{Capabilities: []types.CapabilityTuple{published}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 1 || r.Rows[0].Drift != DriftNone {
		t.Fatalf("per_units 1 vs absent reported as drift: %+v", r.Rows)
	}
}

// A real denominator change is still a price change.
func TestCompute_PerUnitsChangeIsPriceDrift(t *testing.T) {
	cand := tup("openai:chat-completions", "qwen", "paid-job/v1", "830000000000", "https://a", nil)
	cand.PerUnits = 1000
	pub := tup("openai:chat-completions", "qwen", "paid-job/v1", "830000000000", "https://a", nil)
	pub.PerUnits = 0

	r, err := Compute(
		&types.ManifestPayload{Capabilities: []types.CapabilityTuple{cand}},
		&types.ManifestPayload{Capabilities: []types.CapabilityTuple{pub}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 1 || r.Rows[0].Drift != DriftPriceChanged {
		t.Fatalf("per_units 1000 vs absent should be price drift: %+v", r.Rows)
	}
}
