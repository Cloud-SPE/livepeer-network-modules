package sessionengine

import (
	"math/big"
	"strings"
	"testing"
	"time"
)

func describeSpec() *OfferingSpec {
	return &OfferingSpec{
		Capability:          "livepeer:meet/sfu-room",
		Offering:            "default",
		WorkUnit:            "participant_seconds",
		PricePerWorkUnitWei: big.NewInt(10),
		DescriptorSchema:    "sfu-room/v1",
		Metering:            "runner-reported",
		HeartbeatInterval:   10 * time.Second,
		MissedThreshold:     3,
		RunnerPaths:         RunnerPaths{Create: "/sessions", Status: "/sessions/{id}", Terminate: "/sessions/{id}"},
	}
}

func agreeingDescription() *RunnerDescription {
	return &RunnerDescription{
		Protocols: []string{"paid-session/v1"},
		Capabilities: []DescribedCapability{{
			CapabilityID:      "livepeer:meet/sfu-room",
			DescriptorSchemas: []string{"sfu-room/v1"},
			WorkUnit:          "participant_seconds",
			Metering:          "runner-reported",
			Heartbeat:         &DescribedHeartbeat{IntervalSeconds: 5},
			Paths: map[string]string{
				"create": "/sessions", "status": "/sessions/{id}", "terminate": "/sessions/{id}",
			},
		}},
	}
}

func TestCompareDescriptionAgrees(t *testing.T) {
	if got := CompareDescription(describeSpec(), agreeingDescription()); len(got) != 0 {
		t.Fatalf("agreeing declaration produced disagreements: %v", got)
	}
}

// The expensive one: a unit mismatch rejects every usage event for a
// session's lifetime, so it must be caught at configuration time.
func TestCompareDescriptionWorkUnitMismatchIsFatal(t *testing.T) {
	desc := agreeingDescription()
	desc.Capabilities[0].WorkUnit = "participant_minutes"
	got := CompareDescription(describeSpec(), desc)
	fatal := FatalDisagreements(got)
	if len(fatal) != 1 || fatal[0].Field != "work_unit" {
		t.Fatalf("want one fatal work_unit disagreement, got %v", got)
	}
	msg := fatal[0].String()
	// The operator must see both values to fix it.
	if !strings.Contains(msg, "participant_seconds") || !strings.Contains(msg, "participant_minutes") {
		t.Fatalf("message does not name both sides: %s", msg)
	}
}

func TestCompareDescriptionSchemaAndCapabilityAreFatal(t *testing.T) {
	desc := agreeingDescription()
	desc.Capabilities[0].DescriptorSchemas = []string{"rtmp-hls/v1"}
	if f := FatalDisagreements(CompareDescription(describeSpec(), desc)); len(f) != 1 || f[0].Field != "descriptor_schema" {
		t.Fatalf("schema mismatch not fatal: %v", f)
	}

	desc = agreeingDescription()
	desc.Capabilities[0].CapabilityID = "something:else"
	if f := FatalDisagreements(CompareDescription(describeSpec(), desc)); len(f) != 1 || f[0].Field != "capability_id" {
		t.Fatalf("capability mismatch not fatal: %v", f)
	}
}

// Advisory disagreements are reported but must not make a capability
// unservable — the broker's configured values still govern.
func TestCompareDescriptionAdvisoryNotFatal(t *testing.T) {
	desc := agreeingDescription()
	desc.Capabilities[0].Metering = "broker-observed"
	// Emits slower than interval(10s) * threshold(3) = 30s.
	desc.Capabilities[0].Heartbeat = &DescribedHeartbeat{IntervalSeconds: 45}
	desc.Capabilities[0].Paths["status"] = "/v2/sessions/{id}"

	got := CompareDescription(describeSpec(), desc)
	if len(FatalDisagreements(got)) != 0 {
		t.Fatalf("advisory disagreements marked fatal: %v", FatalDisagreements(got))
	}
	fields := map[string]bool{}
	for _, d := range got {
		fields[d.Field] = true
	}
	for _, want := range []string{"metering", "heartbeat", "status_path"} {
		if !fields[want] {
			t.Fatalf("advisory %s not reported: %v", want, got)
		}
	}
}

// A runner that does not self-describe is not in contradiction with
// anything — unreachability must never make a capability unservable.
func TestCompareDescriptionAbsentOrEmpty(t *testing.T) {
	if got := CompareDescription(describeSpec(), nil); len(got) != 0 {
		t.Fatalf("nil description produced disagreements: %v", got)
	}
	if got := CompareDescription(describeSpec(), &RunnerDescription{}); len(got) != 0 {
		t.Fatalf("empty description produced disagreements: %v", got)
	}
}

// A runner serving several capabilities lists them all; the comparison
// must pick the matching one rather than the first.
func TestCompareDescriptionMultiCapabilityRunner(t *testing.T) {
	desc := agreeingDescription()
	desc.Capabilities = append([]DescribedCapability{{
		CapabilityID:      "video:transcode.live",
		DescriptorSchemas: []string{"rtmp-hls/v1"},
		WorkUnit:          "output_seconds",
	}}, desc.Capabilities...)
	if got := CompareDescription(describeSpec(), desc); len(got) != 0 {
		t.Fatalf("multi-capability runner mis-matched: %v", got)
	}
}
