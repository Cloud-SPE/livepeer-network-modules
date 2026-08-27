package server

import (
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/offers"
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
	snap := health.Snapshot{
		ID:         view.CapabilityID,
		OfferingID: view.OfferingID,
		BackendID:  pair.HostID + "|" + pair.LocalID,
		ProbeType:  "attach",
		ProbedAt:   now,
		StaleAfter: now.Add(healthHorizon),
	}
	host, known := s.runners.Get(pair.HostID)
	switch {
	case !known || host.State != "connected":
		snap.Status = health.StatusUnreachable
		snap.Reason = "runner_detached"
		snap.ConsecutiveFailures = 1
	default:
		if _, live := s.runners.ConnFor(pair.HostID, pair.LocalID); !live {
			// The host is attached but this capability's connection is
			// not: the runner dropped the entry without detaching.
			snap.Status = health.StatusUnreachable
			snap.Reason = "capability_not_connected"
			snap.ConsecutiveFailures = 1
			break
		}
		snap.Status = health.StatusReady
		snap.Reason = "certified"
		snap.ConsecutiveSuccesses = 1
		if !host.LastSeen.IsZero() {
			// LastSeen advances on real dispatch, so it is the honest
			// answer to "when did this last demonstrably work". It is
			// reported, but it does not age the verdict: an idle runner
			// is not a failing one, and the tunnel being up is current
			// evidence in a way a probe result never was.
			snap.ProbedAt = host.LastSeen.UTC()
		}
	}
	return snap
}
