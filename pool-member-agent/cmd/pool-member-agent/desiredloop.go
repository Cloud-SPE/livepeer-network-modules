package main

import (
	"context"
	"errors"
	"log"
	"os"
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
	attachCredential string
	mu               sync.RWMutex
	runners          []attach.Runner
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
	return &runnerState{changed: make(chan struct{})}
}

// routes is the local-id route table for the runner set as it is NOW.
// Built per request rather than per tunnel session: on a pool-managed
// host the set comes from desired state and changes while the tunnel
// is up, and a table built at connect would leave a service placed
// afterwards attached but unroutable.
func (s *runnerState) routes() runnerRoutes {
	s.mu.RLock()
	defer s.mu.RUnlock()
	routes := runnerRoutes{}
	for _, r := range s.runners {
		if r.LocalID != "" {
			routes[r.LocalID] = runnerRoute{URL: strings.TrimRight(r.URL, "/"), Bearer: r.LocalBearer}
		}
	}
	return routes
}

func (s *runnerState) set(runners []attach.Runner, revision string) {
	s.mu.Lock()
	s.runners = runners
	s.revision = revision
	if s.changed != nil {
		close(s.changed)
	}
	s.changed = make(chan struct{})
	s.mu.Unlock()
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
		if err := recoverAgentCredentials(ctx, client, cfg, state); err != nil {
			log.Printf("agent credential recovery pending: %v", err)
		}
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
			if cfg.AgentCredentialsFile != "" {
				secrets, err := client.RotateAgentCredentials(ctx, cfg.AgentCredentialsFile)
				if secrets.AttachCredential != "" {
					state.setCredential(secrets.AttachCredential)
				}
				if err != nil {
					log.Printf("credential rotation pending: %v", err)
				}
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
	doc, err = desiredstate.ResolveRuntime(cfg.ComposeFile, doc)
	if err != nil {
		report := desiredstate.StatusReport{Revision: doc.Revision}
		for _, service := range doc.Services {
			report.Services = append(report.Services, desiredstate.ServiceStatus{Name: service.Name, Status: desiredstate.StatusFailed, Detail: "runner secrets require recovery"})
		}
		return errors.Join(err, client.Report(ctx, report))
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
		if service.Stop {
			continue
		}
		out = append(out, attach.Runner{
			LocalID:     service.Name,
			LocalBearer: service.RuntimeBearer,
			URL:         "http://" + service.Name + ":8080",
			Devices:     service.DeviceIDs,
			Draining:    service.Draining,
			RTMPPort:    service.RTMPPort,
		})
	}
	return out
}

// A managed agent reboot advertises hardware only until its controller validates
// current ownership and returns desired state. Existing containers are untouched
// during an outage; a stale local runner file cannot reactivate an old assignment.
func initialRunnerState(cfg config) *runnerState {
	state := newRunnerState()
	state.setCredential(cfg.Credential)
	if !cfg.PoolManaged() {
		state.set(cfg.Runners, "")
	}
	return state
}

func (s *runnerState) setCredential(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attachCredential == value {
		return
	}
	s.attachCredential = value
	if s.changed != nil {
		close(s.changed)
	}
	s.changed = make(chan struct{})
}
func (s *runnerState) credential() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.attachCredential
}
func recoverAgentCredentials(ctx context.Context, client *desiredstate.Client, cfg config, state *runnerState) error {
	if cfg.AgentCredentialsFile == "" {
		return nil
	}
	if _, err := os.Stat(cfg.AgentCredentialsFile); os.IsNotExist(err) {
		secrets, err := client.CurrentAgentCredentials(ctx)
		if err != nil {
			return err
		}
		if err := desiredstate.SaveAgentCredentials(cfg.AgentCredentialsFile, secrets); err != nil {
			return err
		}
	}
	secrets, err := client.RecoverAgentRotation(ctx, cfg.AgentCredentialsFile)
	if secrets.AttachCredential != "" {
		state.setCredential(secrets.AttachCredential)
	}
	return err
}
