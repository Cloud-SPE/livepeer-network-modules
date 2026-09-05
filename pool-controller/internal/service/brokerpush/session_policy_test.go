package brokerpush

import (
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// A session axis authored and dropped is the worst of the three
// outcomes: the operator sets a fixed lease, the template validates,
// the push succeeds, and the broker serves the default. Nothing fails
// and nothing is honoured.
func TestSessionPolicyReachesTheBroker(t *testing.T) {
	tmpl := templates.Template{
		ID: "stream", Capability: "openai:audio-transcriptions", OfferingID: "stream",
		Protocol: "paid-session/v1", PriceDefault: templates.Price{AmountWei: "1", PerUnits: 1},
		Stacking: templates.Stacking{Primary: true},
		SessionPolicy: &templates.SessionPolicy{
			Attachment: "external", Refill: "bounded",
			LeasePolicy: "fixed", LeaseMaxSeconds: 3600,
			MaxRotations: 2,
			Heartbeat:    templates.SessionHeartbeat{IntervalSeconds: 5, MissedThreshold: 3},
		},
	}
	out := BuildOffersFromCatalog([]templates.Template{tmpl},
		[]types.TemplateOverride{{TemplateID: "stream", Enabled: true}})
	if len(out) != 1 {
		t.Fatalf("offers = %d, want 1", len(out))
	}
	policy := out[0].SessionPolicy
	if policy == nil {
		t.Fatal("session_policy did not reach the broker: the pool's lease and heartbeat " +
			"would be silently replaced by defaults")
	}
	if policy.Attachment != "external" || policy.Refill != "bounded" {
		t.Fatalf("attachment/refill = %q/%q", policy.Attachment, policy.Refill)
	}
	if policy.LeasePolicy != "fixed" || policy.LeaseMaxSeconds != 3600 {
		t.Fatalf("lease = %q/%d", policy.LeasePolicy, policy.LeaseMaxSeconds)
	}
	if policy.MaxRotations != 2 {
		t.Fatalf("max_rotations = %d", policy.MaxRotations)
	}
	if policy.Heartbeat == nil || policy.Heartbeat.IntervalSeconds != 5 || policy.Heartbeat.MissedThreshold != 3 {
		t.Fatalf("heartbeat = %+v, want the pool's cadence, not the broker's default", policy.Heartbeat)
	}
}

// A job offering has no lease to bound and no heartbeat to miss, so it
// must carry no policy at all rather than an empty object the broker
// would have to interpret.
func TestAJobOfferCarriesNoSessionPolicy(t *testing.T) {
	out := BuildOffersFromCatalog([]templates.Template{{
		ID: "job", Capability: "openai:chat-completions", OfferingID: "job",
		Protocol: "paid-job/v1", PriceDefault: templates.Price{AmountWei: "1", PerUnits: 1},
		Stacking: templates.Stacking{Primary: true},
	}}, []types.TemplateOverride{{TemplateID: "job", Enabled: true}})
	if out[0].SessionPolicy != nil {
		t.Fatalf("a paid-job offer carries a session policy: %+v", out[0].SessionPolicy)
	}
}
