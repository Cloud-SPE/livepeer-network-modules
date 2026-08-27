package server

import (
	"math"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolsnapshot"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/selection"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
)

// capabilityGroup is one advertised offer plus the attached runners
// eligible to serve it. Published carries the tuple as advertised —
// price and capacity from the operator, everything else from the frozen
// shape — and Backends is one synthesized entry per eligible runner.
type capabilityGroup struct {
	Published *config.Capability
	Backends  []*config.Capability
}

type backendCandidate struct {
	cap         *config.Capability
	weight      int
	maxShareCap float64
	reason      string
}

type backendSelectionDecision struct {
	eligible    bool
	weight      int
	maxShareCap float64
	reason      string
}

// groupFor resolves an advertised offering to the runners serving it.
func (s *Server) groupFor(capID, offID string) (*capabilityGroup, bool) {
	if capID == "" {
		return nil, false
	}
	return s.offerGroupFor(capID, offID)
}

func (s *Server) lookupSpec(capID, offID string) (middleware.CapabilitySpec, bool) {
	group, found := s.groupFor(capID, offID)
	if !found || group.Published == nil {
		return middleware.CapabilitySpec{}, false
	}
	// An offer with no runner attached right now still has a price: the
	// tuple is advertised, so a payment must be validated against it
	// before the dispatch path answers 503.
	return middleware.CapabilitySpec{
		WorkUnit:            group.Published.WorkUnit.Name,
		PricePerWorkUnitWei: mustBig(group.Published.Price.AmountWei),
		PerUnits:            group.Published.Price.PerUnits,
	}, true
}

// selectBackend picks one of the group's eligible runners.
func (s *Server) selectBackend(group *capabilityGroup) (*config.Capability, error) {
	return s.selectRunnerBackend(group)
}

func backendIDForCapability(cap *config.Capability) string {
	if cap == nil {
		return ""
	}
	if cap.Backend.ID != "" {
		return cap.Backend.ID
	}
	return cap.Backend.URL
}

func (s *Server) backendDecision(snap health.Snapshot, pool *poolsnapshot.Status) backendSelectionDecision {
	decision := selection.DecisionFor(snap, pool)
	return backendSelectionDecision{
		eligible:    decision.Eligible,
		weight:      decision.Weight,
		maxShareCap: decision.MaxShareCap,
		reason:      decision.Reason,
	}
}

func summarizeDeniedReasons(reasons map[string]int) string {
	switch len(reasons) {
	case 0:
		return "no_healthy_backend_candidates"
	case 1:
		for reason := range reasons {
			return reason
		}
	}
	return "mixed_denial_reasons"
}

// applyMaxShareCaps bounds any one backend's share of the weighted draw.
//
// The pool controller sets a max share to stop a single member absorbing
// a capability's whole flow — the point is fairness across members, not
// backend health, so it applies to attached runners exactly as it did to
// configured backends. Capping one candidate lowers the total, which can
// push another over its own cap, so this iterates until nothing moves.
func applyMaxShareCaps(candidates []backendCandidate) {
	if len(candidates) < 2 {
		return
	}
	for range candidates {
		changed := false
		total := 0
		for _, candidate := range candidates {
			total += candidate.weight
		}
		for i := range candidates {
			capLimit := clampShareCap(candidates[i].maxShareCap)
			if capLimit <= 0 || capLimit >= 1 {
				continue
			}
			others := total - candidates[i].weight
			if others <= 0 {
				continue
			}
			maxWeight := int(math.Floor((capLimit / (1 - capLimit)) * float64(others)))
			if maxWeight < 1 {
				// Never cap a candidate out of the draw entirely: a
				// share bound is a limit on how much it takes, not a
				// decision that it may not serve.
				maxWeight = 1
			}
			if candidates[i].weight > maxWeight {
				total -= candidates[i].weight - maxWeight
				candidates[i].weight = maxWeight
				changed = true
			}
		}
		if !changed {
			return
		}
	}
}

func clampShareCap(value float64) float64 {
	switch {
	case value <= 0:
		return 0
	case value >= 1:
		return 1
	default:
		return value
	}
}
