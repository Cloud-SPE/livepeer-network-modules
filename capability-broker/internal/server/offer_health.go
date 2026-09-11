package server

import (
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/offers"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runners"
)

// /registry/health over attached runners.
//
// The endpoint's contract is unchanged — the roster, the registry
// daemon's live-health layer and the chain probe all read it — but what
// it reports no longer comes from probing operator-configured URLs.
// For an attached runner the two questions a probe existed to answer
// are already settled: certification says whether it can serve the
// offer, and the attach tunnel says whether it is reachable right now.
// Reporting those directly is both cheaper and more truthful than a
// poll, because there is no window in which the answer is stale.

// healthHorizon is how long a reader may treat this verdict as current.
// The verdict is recomputed from live state on every read, so this is a
// statement about the reader's caching, not about a probe interval.
const healthHorizon = 30 * time.Second

// offerHealth is the broker's health verdict, one entry per advertised
// offer per runner that is eligible to serve it.
func (s *Server) offerHealth() health.Response {
	now := time.Now().UTC()
	out := health.Response{GeneratedAt: now}
	if s.offersEngine == nil {
		out.BrokerStatus = string(health.StatusReady)
		return out
	}
	for _, view := range s.offersEngine.Views() {
		if !view.Advertised || view.Frozen == nil {
			continue
		}
		pairs := s.offersEngine.EligiblePairs(view.OfferingID)
		if len(pairs) == 0 {
			// Advertised with nobody behind it. That is unreachable,
			// not absent: the tuple is still sold, so a reader has to
			// see it and route elsewhere.
			out.Capabilities = append(out.Capabilities, health.Snapshot{
				ID:         view.CapabilityID,
				OfferingID: view.OfferingID,
				Status:     health.StatusUnreachable,
				Reason:     "no_eligible_runner",
				ProbeType:  "attach",
				ProbedAt:   now,
				StaleAfter: now.Add(healthHorizon),
			})
			continue
		}
		for _, pair := range pairs {
			out.Capabilities = append(out.Capabilities, s.pairHealth(view, pair, now))
		}
	}
	out.BrokerStatus = string(health.BrokerStatus(out.Capabilities))
	return out
}

// pairHealth is one eligible runner's verdict for one offer.
func (s *Server) pairHealth(view offers.View, pair offers.PairKey, now time.Time) health.Snapshot {
	host, known := s.runners.Get(pair.HostID)
	_, live := s.runners.ConnFor(pair.HostID, pair.LocalID)
	return attachVerdict(view, pair, host, known, live, now)
}

// attachVerdict is the pure half of pairHealth: the verdict for one
// (offer, runner) pair given what the registry knows about the host.
//
// probed_at is always now and stale_after is always now+healthHorizon.
// The verdict is recomputed from live state on every read, so the
// tunnel being up IS the current evidence, in a way a probe result
// never was — and a reader that ages it against the last dispatch
// would halve an idle runner's selection weight after ninety quiet
// seconds (selection.isNearStale). That was the bug behind lnm-ei4:
// last_seen advances on real dispatch only, and reporting it as
// probed_at made "idle" read as "nearly stale". It is still reported,
// because it is the honest answer to "when did this last demonstrably
// work", but under its own name.
func attachVerdict(view offers.View, pair offers.PairKey, host runners.Snapshot, known, live bool, now time.Time) health.Snapshot {
	snap := health.Snapshot{
		ID:         view.CapabilityID,
		OfferingID: view.OfferingID,
		BackendID:  pair.HostID + "|" + pair.LocalID,
		ProbeType:  "attach",
		ProbedAt:   now,
		StaleAfter: now.Add(healthHorizon),
	}
	switch {
	case !known || host.State != "connected":
		snap.Status = health.StatusUnreachable
		snap.Reason = "runner_detached"
		snap.ConsecutiveFailures = 1
	case !live:
		// The host is attached but this capability's connection is
		// not: the runner dropped the entry without detaching.
		snap.Status = health.StatusUnreachable
		snap.Reason = "capability_not_connected"
		snap.ConsecutiveFailures = 1
	default:
		snap.Status = health.StatusReady
		snap.Reason = "certified"
		snap.ConsecutiveSuccesses = 1
		if !host.LastSeen.IsZero() {
			snap.LastDispatchedAt = host.LastSeen.UTC()
		}
	}
	return snap
}
