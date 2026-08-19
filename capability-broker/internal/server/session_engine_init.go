package server

import (
	"fmt"
	"strings"

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
	engine, err := sessionengine.New(sessionengine.Config{
		Store:   store,
		Payment: s.payment,
		Runner:  s.runnerClientFor,
		Specs: func(sessionID string) *sessionengine.OfferingSpec {
			rec, err := store.Get(sessionID)
			if err != nil {
				return nil
			}
			return s.specForRecord(rec)
		},
		Callback: sessionengine.CallbackConfig{BaseURL: s.cfg.ExternalBaseURL},
	})
	if err != nil {
		_ = store.Close()
		return fmt.Errorf("construct: %w", err)
	}
	s.sessionStore = store
	s.sessionEngine = engine
	return nil
}
