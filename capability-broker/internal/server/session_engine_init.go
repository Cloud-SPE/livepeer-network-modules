package server

import (
	"fmt"
	"os"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionengine"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
)

// initSessionEngine constructs the paid-session engine when any
// capability declares the protocol. Config validation has already
// required session_store and external_base_url in that case.
func (s *Server) initSessionEngine() error {
	if !s.servesProtocol("paid-session/") {
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
		Callback:      sessionengine.CallbackConfig{BaseURL: s.cfg.ExternalBaseURL},
		AllocDebitSeq: s.allocDebitSeq,
		OnWinddown:    s.onSessionWinddown,
	})
	if err != nil {
		_ = store.Close()
		return fmt.Errorf("construct: %w", err)
	}
	s.sessionStore = store
	s.sessionEngine = engine
	return nil
}

// bearerFromAuth resolves an env:// bearer reference for broker→runner
// calls made before the Server exists.
func bearerFromAuth(auth config.AuthConfig) string {
	if auth.Method != "bearer" {
		return ""
	}
	if v, ok := strings.CutPrefix(auth.SecretRef, "env://"); ok {
		return os.Getenv(v)
	}
	return ""
}
