package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/offers"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runners"
)

// Dispatch over eligible attached runners (plan 0043 item 10).
//
// An offer's eligible pairs are its backends. The paid path already
// selects a backend and forwards to a URL, so an attached runner is
// presented as an ordinary backend on the `runner://` scheme rather
// than teaching dispatch a second way to reach one.
//
// What the operator owns stays operator-owned: price and capacity come
// from the offer, never from the runner (plan 0043 §8). What the runner
// declared comes from the FROZEN shape, not from whatever it is saying
// now — the frozen projection is what the signed manifest advertises,
// so it is what the broker must bill against.

// offerGroupFor builds a backend group from an offer's eligible
// runners. Not found means no such offer, or none of its runners are
// eligible right now — which is health (503 + backoff), never a
// manifest change.
func (s *Server) offerGroupFor(capID, offID string) (*capabilityGroup, bool) {
	if s.offersEngine == nil || capID == "" || offID == "" {
		return nil, false
	}
	view, ok := s.offersEngine.ViewOf(offID)
	if !ok || view.Operator.Capability != capID {
		return nil, false
	}
	// Serve the PUBLISHED shape. While a supersession is pending, the
	// accepted shape is what /registry/offerings advertises but the old
	// one is what gateways already bought against (broker-admin §4.3).
	shape := view.Frozen
	if shape == nil {
		return nil, false
	}
	group := &capabilityGroup{FromOffer: true}
	for _, pair := range s.offersEngine.EligiblePairs(offID) {
		cap := s.syntheticCapability(view, shape, pair)
		if cap == nil {
			continue
		}
		if group.Published == nil {
			group.Published = cap
		}
		group.Backends = append(group.Backends, cap)
	}
	if group.Published == nil {
		// The offer exists and is frozen but has nobody serving it. Say
		// so with an empty group so the caller answers 503 rather than
		// 404: the capability IS advertised, it is just unavailable.
		group.Published = s.syntheticPublished(view, shape)
	}
	return group, true
}

// syntheticPublished is the advertised tuple with no backend behind it.
func (s *Server) syntheticPublished(view offers.View, shape *offers.Frozen) *config.Capability {
	cap := &config.Capability{
		ID:          view.Operator.Capability,
		OfferingID:  view.OfferingID,
		Protocol:    shape.Projection.Protocol,
		WorkUnit:    config.WorkUnit{Name: shape.Projection.WorkUnit.Name, Extractor: shape.Projection.WorkUnit.Extractor},
		Price:       view.Operator.Price,
		Extra:       view.Operator.Extra,
		Constraints: view.Operator.Constraints,
	}
	if len(shape.Projection.Transports) > 0 {
		cap.Job = &config.JobCapability{Transports: shape.Projection.Transports}
	}
	return cap
}

// syntheticCapability presents one eligible runner as a backend.
func (s *Server) syntheticCapability(view offers.View, shape *offers.Frozen, pair offers.PairKey) *config.Capability {
	cap := s.syntheticPublished(view, shape)
	cap.Backend = config.Backend{
		ID:          pair.HostID + "|" + pair.LocalID,
		Transport:   "http",
		URL:         runners.BackendURL(pair.HostID, pair.LocalID),
		MaxInFlight: view.Operator.Capacity.MaxInFlight,
		QueueLimit:  view.Operator.Capacity.QueueLimit,
	}
	// The runner's own paths are live data, not part of the frozen
	// shape: a runner may move its endpoint without changing what is
	// sold. Read them from the current document.
	live := s.runnerCapability(pair)
	if live == nil {
		return nil
	}
	if invoke := live.Paths["invoke"]; invoke != "" {
		cap.Backend.URL += invoke
	}
	return cap
}

func (s *Server) runnerCapability(pair offers.PairKey) *runnerLiveCapability {
	sn, ok := s.runners.Get(pair.HostID)
	if !ok || sn.State != "connected" {
		return nil
	}
	for _, cv := range sn.Capabilities {
		if cv.Capability != nil && cv.Capability.LocalID == pair.LocalID {
			return &runnerLiveCapability{Paths: cv.Capability.Paths}
		}
	}
	return nil
}

type runnerLiveCapability struct {
	Paths map[string]string
}

// selectRunnerBackend picks among an offer's eligible runners.
//
// Certification already proved each of these serves the offer, and the
// frozen-shape check already proved it serves the SAME offer, so there
// is no probe verdict to consult here: what remains is capacity and the
// operator's own distribution policy. The weight comes from the pool
// controller's snapshot when there is one — that is the seam the
// fairness ladder feeds (plan 0043 §3.4, pool_snapshot preserved).
func (s *Server) selectRunnerBackend(group *capabilityGroup) (*config.Capability, error) {
	if group == nil || len(group.Backends) == 0 {
		return nil, fmt.Errorf("no eligible runner is attached")
	}
	capID, offID := group.Published.ID, group.Published.OfferingID
	candidates := make([]backendCandidate, 0, len(group.Backends))
	denied := map[string]int{}
	for _, cap := range group.Backends {
		backendID := backendIDForCapability(cap)
		if cap.Backend.MaxInFlight > 0 && s.currentBackendInFlight(backendID) >= cap.Backend.MaxInFlight {
			const reason = "max_in_flight_reached"
			observability.RecordBackendSelectionDenied(capID, offID, backendID, reason)
			denied[reason]++
			continue
		}
		weight, reason := s.runnerSelectionWeight(backendID, capID, offID)
		if weight <= 0 {
			observability.RecordBackendSelectionDenied(capID, offID, backendID, reason)
			denied[reason]++
			continue
		}
		candidates = append(candidates, backendCandidate{cap: cap, weight: weight, maxShareCap: 0, reason: reason})
	}
	if len(candidates) == 0 {
		observability.RecordBackendSelectionExhausted(capID, offID, summarizeDeniedReasons(denied))
		return nil, fmt.Errorf("no eligible runner has capacity")
	}
	pick := candidates[0]
	if len(candidates) > 1 {
		total := 0
		for _, c := range candidates {
			total += c.weight
		}
		pickFn := s.randIntn
		if pickFn == nil {
			pickFn = func(int) int { return 0 }
		}
		n := pickFn(total)
		for _, c := range candidates {
			if n < c.weight {
				pick = c
				break
			}
			n -= c.weight
		}
	}
	observability.RecordBackendSelectionFinal(capID, offID, backendIDForCapability(pick.cap), pick.reason)
	return pick.cap, nil
}

// runnerSelectionWeight consults the pool snapshot for this runner,
// falling back to an equal share. A snapshot that explicitly excludes a
// runner is honoured; a snapshot that has never heard of it is not
// treated as a denial — a standalone broker has no snapshot at all.
func (s *Server) runnerSelectionWeight(backendID, capID, offID string) (int, string) {
	snapshot := s.currentPoolSnapshot()
	if snapshot == nil {
		return 1, "no_pool_snapshot"
	}
	status := snapshot.StatusFor(backendID, capID, offID)
	if !status.Configured {
		return 1, "not_in_pool_snapshot"
	}
	decision := s.backendDecision(readyRunnerSnapshot(), &status)
	if !decision.eligible {
		return 0, decision.reason
	}
	return decision.weight, decision.reason
}

// readyRunnerSnapshot is the health verdict for an eligible attached
// runner: ready. Certification proved it serves this offer, so the
// probe machinery that exists for operator-configured backend URLs has
// nothing left to decide here — but the pool-snapshot decision function
// takes a probe snapshot, so it gets an honest one.
func readyRunnerSnapshot() health.Snapshot {
	return health.Snapshot{Status: health.StatusReady, Reason: "certified"}
}

// runnerForwarder claims the runner:// scheme and delegates everything
// else, so the forwarder chain stays a chain of single-purpose links.
type runnerForwarder struct {
	next     backend.Forwarder
	registry *runners.Registry
}

func (f runnerForwarder) Forward(ctx context.Context, req backend.ForwardRequest) (*http.Response, error) {
	if u, err := url.Parse(req.URL); err == nil && u.Scheme == runners.VirtualScheme {
		return f.registry.Forward(ctx, req)
	}
	if f.next == nil {
		return nil, fmt.Errorf("no fallback forwarder configured")
	}
	return f.next.Forward(ctx, req)
}

var _ backend.Forwarder = runnerForwarder{}

// servesProtocol reports whether either grammar declares a protocol, so
// the paid surface is registered for an offers-only broker too. Scanning
// capabilities[] alone meant an offer could be advertised, frozen, and
// certified while POST /v1/job was never routed.
func (s *Server) servesProtocol(prefix string) bool {
	cfg := s.currentConfig()
	if cfg == nil {
		return false
	}
	for i := range cfg.Capabilities {
		if strings.HasPrefix(cfg.Capabilities[i].Protocol, prefix) {
			return true
		}
	}
	for i := range cfg.Offers {
		if strings.HasPrefix(cfg.Offers[i].Protocol, prefix) {
			return true
		}
	}
	// An admin-sourced broker has no offers in its file: the controller
	// pushes them after startup, so the surface must already be there.
	// Only claim what this broker can actually stand up, though — a
	// paid-session surface without a durable store would fail at
	// startup for offers that may never arrive. A session offer pushed
	// to a broker with no store is refused by the push validation with
	// that exact reason.
	if cfg.OffersSource != config.OffersSourceAdmin {
		return false
	}
	if prefix == "paid-session/" {
		return cfg.SessionStore.Path != "" && cfg.ExternalBaseURL != ""
	}
	return true
}
