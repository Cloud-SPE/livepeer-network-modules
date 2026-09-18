package server

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/offers"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runners"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/selection"
)

var (
	healthView = offers.View{OfferingID: "qwen3.6-27b", CapabilityID: "openai:chat-completions"}
	healthPair = offers.PairKey{HostID: "ai2-rig", LocalID: "qwen-chat", OfferingID: "qwen3.6-27b"}
)

// lnm-ei4: a runner that certified and has sat idle since must keep its
// full selection weight. Idleness is not staleness; the tunnel being up
// is the evidence.
func TestAttachVerdictIdleRunnerIsNotNearStale(t *testing.T) {
	now := time.Now().UTC()
	lastSeen := now.Add(-10 * time.Minute)
	host := runners.Snapshot{HostID: "ai2-rig", State: "connected", LastSeen: lastSeen}

	snap := attachVerdict(healthView, healthPair, host, true, true, now)

	if snap.Status != health.StatusReady || snap.Reason != "certified" {
		t.Fatalf("verdict = %s/%s, want ready/certified", snap.Status, snap.Reason)
	}
	if !snap.ProbedAt.Equal(now) {
		t.Fatalf("probed_at = %s, want now (%s): the verdict must not be aged by the last dispatch", snap.ProbedAt, now)
	}
	if !snap.StaleAfter.Equal(now.Add(healthHorizon)) {
		t.Fatalf("stale_after = %s, want now+%s", snap.StaleAfter, healthHorizon)
	}
	if !snap.LastDispatchedAt.Equal(lastSeen) {
		t.Fatalf("last_dispatched_at = %s, want %s", snap.LastDispatchedAt, lastSeen)
	}
	if snap.BackendID != "ai2-rig|qwen-chat" || snap.ProbeType != "attach" {
		t.Fatalf("identity: backend=%q probe=%q", snap.BackendID, snap.ProbeType)
	}

	d := selection.DecisionFor(snap, nil)
	if !d.Eligible || d.Reason != "eligible" {
		t.Fatalf("selection = eligible:%v reason:%q, want eligible/eligible", d.Eligible, d.Reason)
	}
	fresh := attachVerdict(healthView, healthPair, runners.Snapshot{State: "connected", LastSeen: now}, true, true, now)
	if fw := selection.DecisionFor(fresh, nil).Weight; d.Weight != fw {
		t.Fatalf("idle weight %d != freshly dispatched weight %d", d.Weight, fw)
	}
}

func TestAttachVerdictUnreachableCases(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name        string
		host        runners.Snapshot
		known, live bool
		reason      string
	}{
		{"unknown host", runners.Snapshot{}, false, false, "runner_detached"},
		{"disconnected host", runners.Snapshot{State: "disconnected"}, true, false, "runner_detached"},
		{"capability dropped", runners.Snapshot{State: "connected", LastSeen: now}, true, false, "capability_not_connected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := attachVerdict(healthView, healthPair, tc.host, tc.known, tc.live, now)
			if snap.Status != health.StatusUnreachable || snap.Reason != tc.reason {
				t.Fatalf("verdict = %s/%s, want unreachable/%s", snap.Status, snap.Reason, tc.reason)
			}
			if !snap.LastDispatchedAt.IsZero() {
				t.Fatalf("last_dispatched_at reported on an unreachable backend: %s", snap.LastDispatchedAt)
			}
			if selection.DecisionFor(snap, nil).Eligible {
				t.Fatal("unreachable backend selected")
			}
		})
	}
}
