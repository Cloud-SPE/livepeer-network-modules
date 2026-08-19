package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionengine"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
)

// initSessionEngine constructs the paid-session engine when any
// capability declares the protocol. Config validation has already
// required session_store and external_base_url in that case.
func (s *Server) initSessionEngine() error {
	hasSession := false
	for i := range s.cfg.Capabilities {
		if strings.HasPrefix(s.cfg.Capabilities[i].Protocol, "paid-session/") {
			hasSession = true
			break
		}
	}
	if !hasSession {
		return nil
	}
	key, err := sessionstore.LoadKeyFile(s.cfg.SessionStore.SealingKeyFile)
	if err != nil {
		return err
	}
	store, err := sessionstore.Open(s.cfg.SessionStore.Path, key)
	if err != nil {
		return err
	}
	s.sessionWS = newSessionWSHub()
	engine, err := sessionengine.New(sessionengine.Config{
		Store:   store,
		Payment: s.payment,
		Runner:  s.runnerClientFor,
		OnEvent: s.onEngineEvent,
		Specs: func(sessionID string) *sessionengine.OfferingSpec {
			rec, err := store.Get(sessionID)
			if err != nil {
				return nil
			}
			return s.specForRecord(rec)
		},
		Callback:   sessionengine.CallbackConfig{BaseURL: s.cfg.ExternalBaseURL},
		OnWinddown: observability.RecordSessionWinddown,
	})
	if err != nil {
		_ = store.Close()
		return fmt.Errorf("construct: %w", err)
	}
	s.sessionStore = store
	s.sessionEngine = engine
	return s.checkRunnerDescriptions()
}

// checkRunnerDescriptions reads each paid-session runner's own
// declaration and compares it against the offering configured against
// it (paid-session/v1 §7.1.1). A contradiction means the configuration
// is already broken — sessions would fail at open, or every usage event
// would be rejected — so the broker refuses to start rather than
// advertise something that cannot work. A runner that cannot be reached
// is only a warning: unreachability is not a contradiction.
//
// Declarations are never adopted. Published offerings are cold-key
// signed, so absorbing a runner's values would let a runner-side change
// alter what the orchestrator sells.
func (s *Server) checkRunnerDescriptions() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var fatal []string
	for i := range s.cfg.Capabilities {
		c := &s.cfg.Capabilities[i]
		if c.Session == nil || c.Session.Runner.DescribePath == "" {
			continue
		}
		client, ok := s.runnerClientFor(c.ID + "|" + c.OfferingID).(*sessionengine.HTTPRunnerClient)
		if !ok {
			continue
		}
		desc, err := client.Describe(ctx, c.Session.Runner.DescribePath)
		if err != nil {
			log.Printf("warning: %s/%s: runner describe failed: %v (continuing; unreachability is not a contradiction)",
				c.ID, c.OfferingID, err)
			continue
		}
		if desc == nil {
			continue
		}
		for _, d := range sessionengine.CompareDescription(specFromCapability(c), desc) {
			if d.Fatal {
				fatal = append(fatal, d.String())
			} else {
				log.Printf("warning: %s", d.String())
			}
		}
	}
	if len(fatal) > 0 {
		return fmt.Errorf("runner self-description contradicts configuration:\n  %s",
			strings.Join(fatal, "\n  "))
	}
	return nil
}
