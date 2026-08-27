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
}

func (s *runnerState) set(runners []attach.Runner, revision string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runners = runners
	s.revision = revision
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
func runnersFor(doc desiredstate.Document) []attach.Runner {
	out := make([]attach.Runner, 0, len(doc.Services))
	for _, service := range doc.Services {
		out = append(out, attach.Runner{
			LocalID:  service.Name,
			URL:      "http://" + service.Name + ":8080",
			Devices:  service.DeviceIDs,
			Draining: service.Draining,
			Profile:  profileFor(service.TemplateID),
		})
	}
	return out
}

// profileFor picks the capability shape from the template id. The
// template names the workload family, and the agent only needs to know
// which wire shape to declare.
func profileFor(templateID string) string {
	if strings.Contains(templateID, "transcode") {
		return attach.ProfileTranscode
	}
	return attach.ProfileOpenAICompatible
}
