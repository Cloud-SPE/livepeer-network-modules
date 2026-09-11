package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runners"
)

// runnerGroup builds the group offer dispatch would build for two
// eligible attached runners under one offering.
func runnerGroup(capID, offID string, locals ...string) *capabilityGroup {
	published := &config.Capability{
		ID: capID, OfferingID: offID, Protocol: "paid-job/v1",
		Price: config.Price{AmountWei: "1", PerUnits: 1},
	}
	group := &capabilityGroup{Published: published}
	for _, local := range locals {
		cap := *published
		cap.Backend = config.Backend{
			ID:  "host|" + local,
			URL: runners.BackendURL("host", local),
		}
		entry := cap
		group.Backends = append(group.Backends, &entry)
	}
	return group
}

// The pool controller's max-share cap bounds how much of a capability
// any one member absorbs. It is a fairness policy, not a health verdict,
// so it must bind on attached runners exactly as it did on
// operator-configured backends — certification decides who MAY serve,
// the snapshot decides how much they get.
func TestSelectRunnerBackendEnforcesPoolMaxShareCap(t *testing.T) {
	const capID, offID = "openai:chat-completions", "shared"
	s := &Server{
		cfg:      &config.Config{PoolSnapshot: config.PoolSnapshot{URL: "http://pool-controller:8080"}},
		randIntn: func(int) int { return 50 },
	}
	// Equal scores; only runner-a is capped. A draw at 50/100 lands on
	// the capped runner unless the cap has actually lowered its weight.
	s.poolSnapshot = loadTestPoolSnapshot(t, fmt.Sprintf(`{
		"generated_at":%q,
		"entries":[
			{"backend_id":"host|a","capability_id":%q,"offering_id":%q,"state":"eligible","effective_selection_score":1.0,"max_share_cap":0.20},
			{"backend_id":"host|b","capability_id":%q,"offering_id":%q,"state":"eligible","effective_selection_score":1.0}
		]
	}`, time.Now().UTC().Format(time.RFC3339), capID, offID, capID, offID))

	selected, err := s.selectRunnerBackend(runnerGroup(capID, offID, "a", "b"))
	if err != nil {
		t.Fatalf("selectRunnerBackend() error = %v", err)
	}
	if got := selected.Backend.ID; got != "host|b" {
		t.Fatalf("selected = %q, want host|b — the cap on host|a was not applied", got)
	}
}

// A snapshot that excludes a runner is honoured; a broker with no
// snapshot at all treats every eligible runner as an equal share,
// because a standalone deployment has no fairness policy to consult.
func TestRunnerSelectionWeightWithoutPoolSnapshot(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	weight, maxShare, reason := s.runnerSelectionWeight("host|a", "cap", "off")
	if weight != 1 || maxShare != 0 || reason != "no_pool_snapshot" {
		t.Fatalf("runnerSelectionWeight() = %d, %v, %q", weight, maxShare, reason)
	}
}

// Capping must never remove a candidate from the draw: a share bound
// says how much a runner may take, not that it may not serve.
func TestApplyMaxShareCapsKeepsEveryCandidateDrawable(t *testing.T) {
	candidates := []backendCandidate{
		{weight: 1000, maxShareCap: 0.001},
		{weight: 1, maxShareCap: 0},
	}
	applyMaxShareCaps(candidates)
	for i, c := range candidates {
		if c.weight < 1 {
			t.Fatalf("candidate %d weight = %d, want >= 1", i, c.weight)
		}
	}
	if candidates[0].weight >= 1000 {
		t.Fatalf("capped candidate weight = %d, want lowered from 1000", candidates[0].weight)
	}
}
