package main

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-member-agent/internal/attach"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-member-agent/internal/desiredstate"
)

// The desired-state loop (plan 0044 §3.4).
//
// It runs alongside the attach tunnel, not inside it: the two answer to
// different things. The tunnel keeps the broker's view of what this
// host serves current; this keeps the host's containers matching what
// the pool decided. A broker restart must not stop the host reconciling,
// and a controller outage must not drop the tunnel.

// runnerState is the runner set the attach document is built from. The
// desired-state loop replaces it as placements change, and the tunnel
// loop reads it on every re-register.
type runnerState struct {
	mu      sync.RWMutex
	runners []attach.Runner
	// revision is the desired state these runners came from, logged so
	// an operator can tie a running container to a pool decision.
	revision string
	// changed wakes a live tunnel session. Without it a drain would sit
	// unannounced until the next refresh tick, and the broker would
	// keep dispatching to a runner the pool has already withdrawn for
	// as long as that tick is — which is precisely the window
	// runner-attach §7.1 exists to close.
	changed chan struct{}
}

func newRunnerState() *runnerState {
	return &runnerState{changed: make(chan struct{}, 1)}
}

// routes is the local-id route table for the runner set as it is NOW.
// Built per request rather than per tunnel session: on a pool-managed
// host the set comes from desired state and changes while the tunnel
// is up, and a table built at connect would leave a service placed
// afterwards attached but unroutable.
func (s *runnerState) routes() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return attach.RouteTable(s.runners)
}

func (s *runnerState) set(runners []attach.Runner, revision string) {
	s.mu.Lock()
	s.runners = runners
	s.revision = revision
	ch := s.changed
	s.mu.Unlock()
	if ch == nil {
		return
	}
	// Non-blocking: the channel carries "something changed", not a
	// queue of changes, so a session that has not woken yet will pick
	// up the latest state when it does.
	select {
	case ch <- struct{}{}:
	default:
	}
}

// wake is the signal a live session waits on.
func (s *runnerState) wake() <-chan struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.changed
}

func (s *runnerState) get() ([]attach.Runner, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]attach.Runner(nil), s.runners...), s.revision
}

// desiredLoop polls the controller and makes the host match.
//
// Reconciling is best-effort and never fatal: a controller that is down
// leaves the host running exactly what it was running, which is the
// right answer — the last known desired state is still the pool's most
// recent instruction, and tearing containers down because the
// controller is unreachable would turn a control-plane outage into a
// data-plane one.
func desiredLoop(ctx context.Context, cfg config, state *runnerState, reattach func()) {
	client := desiredstate.New(cfg.ControllerURL, cfg.EnrollmentID, cfg.EnrollmentToken, cfg.PollTimeout)
	runner := desiredstate.ComposeRunner{Binary: cfg.ComposeBinary, Args: cfg.ComposeArgs}
	ticker := time.NewTicker(cfg.PollEvery)
	defer ticker.Stop()
	// Rotate on a cadence well inside any plausible token lifetime.
	// A host that waits for expiry has already stopped earning by the
	// time anyone can act.
	rotate := time.NewTicker(cfg.RotateEvery)
	defer rotate.Stop()
	for {
		if err := reconcileOnce(ctx, client, runner, cfg, state, reattach); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Printf("desired-state reconcile failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-rotate.C:
			if _, err := client.Rotate(ctx, cfg.EnrollmentTokenFile); err != nil {
				log.Printf("credential rotation failed: %v", err)
			} else {
				log.Printf("enrollment credential rotated")
			}
		case <-ticker.C:
		}
	}
}

func reconcileOnce(ctx context.Context, client *desiredstate.Client, runner desiredstate.Runner,
	cfg config, state *runnerState, reattach func()) error {

	doc, err := client.Fetch(ctx)
	if errors.Is(err, desiredstate.ErrUnchanged) {
		return nil
	}
	if err != nil {
		// A 401 means this host's token no longer works. Rotating on
		// the strength of a token that has already been rejected is
		// pointless, so this is the one failure the agent cannot
		// recover from on its own — it says so rather than retrying
		// silently until someone notices the host stopped earning.
		if strings.Contains(err.Error(), "401") {
			log.Printf("desired-state: enrollment token rejected; this host needs re-enrolling from the member portal")
		}
		return err
	}
	log.Printf("desired state %s: %d service(s)", doc.Revision, len(doc.Services))

	// Tell the broker BEFORE touching containers. A service the pool is
	// withdrawing has to stop receiving work before it stops serving
	// it, or requests already dispatched are dropped on the floor
	// (runner-attach §7.1).
	state.set(runnersFor(doc), doc.Revision)
	if reattach != nil {
		reattach()
	}

	report := desiredstate.Apply(ctx, runner, cfg.ComposeFile, doc)
	if err := client.Report(ctx, report); err != nil {
		return err
	}
	return nil
}

// runnersFor maps desired services onto attach runner entries.
//
// The URL is the compose service name: the agent reaches its own
// containers on the compose network, and that address never leaves this
// host — the broker sees only the tunnel.
// runnersFor turns the pool's desired state into the host's half of the
// attach document. What each service SERVES is not in here and never
// was the controller's to say: the runner states it in its contract,
// which the agent fetches from the service at attach (attach.Resolve).
// The controller's capability/identity on the service are for the
// member's own reporting and the operator's correlation.
func runnersFor(doc desiredstate.Document) []attach.Runner {
	out := make([]attach.Runner, 0, len(doc.Services))
	for _, service := range doc.Services {
		out = append(out, attach.Runner{
			LocalID:  service.Name,
			URL:      "http://" + service.Name + ":8080",
			Devices:  service.DeviceIDs,
			Draining: service.Draining,
		})
	}
	return out
}
