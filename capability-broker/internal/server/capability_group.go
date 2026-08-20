package server

import (
	"fmt"
	"math"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolsnapshot"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/selection"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
)

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

func (s *Server) groupFor(capID, offID string) (*capabilityGroup, bool) {
	if capID == "" {
		return nil, false
	}
	cfg := s.currentConfig()
	if cfg == nil {
		return nil, false
	}
	group := &capabilityGroup{}
	var fallback *config.Capability
	for i := range cfg.Capabilities {
		c := &cfg.Capabilities[i]
		if c.ID != capID {
			continue
		}
		if fallback == nil {
			fallback = c
		}
		if c.OfferingID == offID {
			if group.Published == nil {
				group.Published = c
			}
			group.Backends = append(group.Backends, c)
		}
	}
	if fallback == nil {
		return nil, false
	}
	if group.Published == nil {
		group.Published = fallback
	}
	if len(group.Backends) == 0 {
		return &capabilityGroup{Published: group.Published}, true
	}
	return group, true
}

func (s *Server) lookupSpec(capID, offID string) (middleware.CapabilitySpec, bool) {
	group, found := s.groupFor(capID, offID)
	if !found || group.Published == nil || len(group.Backends) == 0 {
		return middleware.CapabilitySpec{}, false
	}
	return middleware.CapabilitySpec{
		WorkUnit:            group.Published.WorkUnit.Name,
		PricePerWorkUnitWei: mustBig(group.Published.Price.AmountWei),
		PerUnits:            group.Published.Price.PerUnits,
	}, true
}

func (s *Server) selectBackend(group *capabilityGroup) (*config.Capability, error) {
	if group == nil || len(group.Backends) == 0 {
		return nil, fmt.Errorf("no backend candidates")
	}
	candidates := make([]backendCandidate, 0, len(group.Backends))
	deniedReasons := map[string]int{}
	healthMgr := s.currentHealth()
	if healthMgr == nil {
		return nil, fmt.Errorf("health manager is not available")
	}
	snapshots := healthMgr.SnapshotsFor(group.Published.ID, group.Published.OfferingID)
	byBackend := map[string]health.Snapshot{}
	for _, snap := range snapshots {
		byBackend[snap.BackendID] = snap
	}
	for _, cap := range group.Backends {
		backendID := backendIDForCapability(cap)
		if cap.Backend.MaxInFlight > 0 && s.currentBackendInFlight(backendID) >= cap.Backend.MaxInFlight {
			const reason = "max_in_flight_reached"
			observability.RecordBackendSelectionDenied(cap.ID, cap.OfferingID, backendID, reason)
			deniedReasons[reason]++
			continue
		}
		snap, ok := byBackend[backendID]
		if !ok {
			snap = health.Snapshot{Status: health.StatusStale}
		}
		var poolStatus *poolsnapshot.Status
		if poolSnapshot := s.currentPoolSnapshot(); poolSnapshot != nil {
			if status := poolSnapshot.StatusFor(backendID, group.Published.ID, group.Published.OfferingID); status.Configured {
				poolStatus = &status
			}
		}
		decision := s.backendDecision(snap, poolStatus)
		if !decision.eligible || decision.weight == 0 {
			observability.RecordBackendSelectionDenied(cap.ID, cap.OfferingID, backendID, decision.reason)
			deniedReasons[decision.reason]++
			continue
		}
		candidates = append(candidates, backendCandidate{cap: cap, weight: decision.weight, maxShareCap: decision.maxShareCap, reason: decision.reason})
	}
	if len(candidates) == 0 {
		observability.RecordBackendSelectionExhausted(group.Published.ID, group.Published.OfferingID, summarizeDeniedReasons(deniedReasons))
		return nil, fmt.Errorf("no healthy backend candidates")
	}
	if len(candidates) == 1 {
		backendID := backendIDForCapability(candidates[0].cap)
		observability.RecordBackendSelectionFinal(group.Published.ID, group.Published.OfferingID, backendID, candidates[0].reason)
		return candidates[0].cap, nil
	}
	applyMaxShareCaps(candidates)
	total := 0
	for _, c := range candidates {
		total += c.weight
	}
	pickFn := s.randIntn
	if pickFn == nil {
		pickFn = func(n int) int { return 0 }
	}
	pick := pickFn(total)
	for _, c := range candidates {
		if pick < c.weight {
			backendID := backendIDForCapability(c.cap)
			observability.RecordBackendSelectionFinal(group.Published.ID, group.Published.OfferingID, backendID, c.reason)
			return c.cap, nil
		}
		pick -= c.weight
	}
	last := candidates[len(candidates)-1]
	backendID := backendIDForCapability(last.cap)
	observability.RecordBackendSelectionFinal(group.Published.ID, group.Published.OfferingID, backendID, last.reason)
	return last.cap, nil
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

func backendSelectionWeight(snap health.Snapshot) int {
	return selection.ProbeWeight(snap)
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

func backendSelectionDenyReason(snap health.Snapshot) string {
	return selection.ProbeReason(snap)
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
