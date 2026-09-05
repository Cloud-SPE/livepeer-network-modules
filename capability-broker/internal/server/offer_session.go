package server

import (
	"context"
	"time"

	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/offers"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runners"
)

// paid-session over attached runners.
//
// The job path presents an eligible runner as an ordinary backend and
// lets dispatch forward to it. Sessions cannot do only that: the engine
// holds a session open across create/status/terminate, and every one of
// those calls has to reach the SAME runner. So an offer-sourced session
// spec pins its runner into the backend ref at open, and resolves that
// ref — not the offer — on every later call.

// sessionBackendRef is "capability|offering|host|local". The two-field
// form is the operator-configured backend.
func sessionBackendRef(capID, offID string, pair offers.PairKey) string {
	return capID + "|" + offID + "|" + pair.HostID + "|" + pair.LocalID
}

// splitSessionBackendRef returns the pinned pair, if the ref carries one.
func splitSessionBackendRef(ref string) (capID, offID string, pair offers.PairKey, pinned bool) {
	parts := strings.SplitN(ref, "|", 4)
	if len(parts) != 4 {
		capID, offID, _ = strings.Cut(ref, "|")
		return capID, offID, offers.PairKey{}, false
	}
	return parts[0], parts[1], offers.PairKey{HostID: parts[2], LocalID: parts[3]}, true
}

// offerSessionCapability builds the session tuple an offer serves,
// pinned to one eligible attached runner. Nil means the offer is not a
// frozen paid-session offer, or nothing is serving it right now; the
// caller separates those with sessionOfferAdvertised.
func (s *Server) offerSessionCapability(capID, offID string) *config.Capability {
	group, ok := s.offerGroupFor(capID, offID)
	if !ok || group.Published == nil || !strings.HasPrefix(group.Published.Protocol, "paid-session/") {
		return nil
	}
	cap, err := s.selectRunnerBackend(group)
	if err != nil {
		return nil
	}
	return cap
}

// pinnedSessionCapability rebuilds the tuple for a session already bound
// to a runner. It deliberately does not re-select: a live session must
// keep talking to the runner that holds it, and if that runner is gone
// the honest answer is an unroutable client, not a different runner.
func (s *Server) pinnedSessionCapability(capID, offID string, pair offers.PairKey) *config.Capability {
	if s.offersEngine == nil {
		return nil
	}
	view, ok := s.offersEngine.ViewOf(offID)
	if !ok || view.Operator.Capability != capID || view.Frozen == nil {
		return nil
	}
	// Built from the FROZEN shape, not from a live connection: a
	// rehydrating broker has to price and close a session it holds
	// before the runner has finished re-attaching, and refusing to
	// resolve the spec then would leave that session stuck active.
	if cap := s.syntheticCapability(view, view.Frozen, pair); cap != nil {
		return cap
	}
	cap := s.syntheticPublished(view, view.Frozen)
	if cap == nil || cap.Session == nil {
		return nil
	}
	cap.Backend = config.Backend{
		ID:          pair.HostID + "|" + pair.LocalID,
		Transport:   "http",
		URL:         runners.BackendURL(pair.HostID, pair.LocalID),
		MaxInFlight: view.Operator.Capacity.MaxInFlight,
		QueueLimit:  view.Operator.Capacity.QueueLimit,
	}
	return cap
}

// sessionOfferAdvertised reports whether a frozen paid-session offer
// exists under these ids, whatever is serving it. An advertised offering
// with no eligible runner is unavailable (503), not absent (404).
func (s *Server) sessionOfferAdvertised(capID, offID string) bool {
	group, ok := s.offerGroupFor(capID, offID)
	return ok && group.Published != nil && strings.HasPrefix(group.Published.Protocol, "paid-session/")
}

// runnerRoundTripper routes runner:// URLs over the attach tunnel, so
// the session engine's ordinary HTTP client can reach an attached
// runner without learning that attached runners exist.
type runnerRoundTripper struct{ registry *runners.Registry }

func (t runnerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil || req.URL.Scheme != runners.VirtualScheme {
		return nil, fmt.Errorf("runner transport: not a %s:// URL", runners.VirtualScheme)
	}
	if t.registry == nil {
		return nil, fmt.Errorf("runner transport: no registry")
	}
	var body io.Reader
	if req.Body != nil {
		body = req.Body
		defer func() { _ = req.Body.Close() }()
	}
	return t.registry.Forward(req.Context(), backend.ForwardRequest{
		URL:     req.URL.String(),
		Method:  req.Method,
		Headers: req.Header.Clone(),
		Body:    body,
	})
}

var _ http.RoundTripper = runnerRoundTripper{}

// runnerHTTPClient returns a client that can reach a runner:// URL.
func (s *Server) runnerHTTPClient(rawURL string) *http.Client {
	if u, err := url.Parse(rawURL); err != nil || u.Scheme != runners.VirtualScheme {
		return nil
	}
	return &http.Client{Transport: runnerRoundTripper{registry: s.runners}}
}

// onRunnerAttached re-runs session recovery now that a runner is
// reachable.
//
// The engine recovers once at startup — before any runner can have
// re-attached. A session held by a connected runner is therefore
// unreachable at exactly the moment recovery looks at it, so it takes
// the "runner unreachable, leave active" branch and stays active until
// the heartbeat sweep eventually fails it closed. That is too slow for
// a session the broker can no longer bill. Recovery is idempotent, so
// running it again the moment the runner is back closes the window.
func (s *Server) onRunnerAttached() {
	if s.sessionEngine == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		s.sessionEngine.Recover(ctx)
	}()
}
