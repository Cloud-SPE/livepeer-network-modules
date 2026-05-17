package server

import (
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
)

type capabilityGroup struct {
	Published *config.Capability
	Backends  []*config.Capability
}

func (s *Server) groupFor(capID, offID string) (*capabilityGroup, bool) {
	if capID == "" {
		return nil, false
	}
	group := &capabilityGroup{}
	for i := range s.cfg.Capabilities {
		c := &s.cfg.Capabilities[i]
		if c.ID != capID {
			continue
		}
		if group.Published == nil {
			group.Published = c
		}
		if c.OfferingID == offID {
			group.Backends = append(group.Backends, c)
		}
	}
	if group.Published == nil {
		return nil, false
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
	}, true
}

func (s *Server) selectBackend(group *capabilityGroup) (*config.Capability, error) {
	if group == nil || len(group.Backends) == 0 {
		return nil, fmt.Errorf("no backend candidates")
	}
	type candidate struct {
		cap    *config.Capability
		weight int
	}
	candidates := make([]candidate, 0, len(group.Backends))
	snapshots := s.health.SnapshotsFor(group.Published.ID, group.Published.OfferingID)
	byBackend := map[string]health.Snapshot{}
	for _, snap := range snapshots {
		byBackend[snap.BackendID] = snap
	}
	for _, cap := range group.Backends {
		backendID := cap.Backend.ID
		if backendID == "" {
			backendID = cap.Backend.URL
		}
		snap, ok := byBackend[backendID]
		if !ok {
			snap = health.Snapshot{Status: health.StatusStale}
		}
		weight := backendSelectionWeight(snap)
		if weight == 0 {
			continue
		}
		candidates = append(candidates, candidate{cap: cap, weight: weight})
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no healthy backend candidates")
	}
	if len(candidates) == 1 {
		return candidates[0].cap, nil
	}
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
			return c.cap, nil
		}
		pick -= c.weight
	}
	return candidates[len(candidates)-1].cap, nil
}

func backendSelectionWeight(snap health.Snapshot) int {
	base := 0
	switch snap.Status {
	case health.StatusReady:
		base = 100
	case health.StatusDegraded:
		base = 25
	default:
		return 0
	}
	if !snap.StaleAfter.IsZero() && snap.StaleAfter.Before(nowUTC()) {
		return 0
	}
	successBonus := minInt(snap.ConsecutiveSuccesses, 5) * 10
	failurePenalty := minInt(snap.ConsecutiveFailures, 5) * 15
	weight := base + successBonus - failurePenalty
	if weight <= 0 {
		return 0
	}
	if isNearStale(snap) {
		weight /= 2
	}
	if weight <= 0 {
		return 1
	}
	return weight
}
