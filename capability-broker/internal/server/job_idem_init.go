package server

import (
	"log"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
)

// initJobIdem selects the paid-job idempotency store: the durable
// state store when configured (spec-conformant), an in-process map
// with a logged warning otherwise.
func (s *Server) initJobIdem() error {
	if !s.servesProtocol("paid-job/") {
		return nil
	}
	if s.sessionStore != nil {
		s.jobIdem = &boltJobIdem{store: s.sessionStore}
		return nil
	}
	if s.cfg.SessionStore.Path != "" {
		key, err := sessionstore.LoadKeyFile(s.cfg.SessionStore.SealingKeyFile)
		if err != nil {
			return err
		}
		store, err := sessionstore.Open(s.cfg.SessionStore.Path, key)
		if err != nil {
			return err
		}
		s.sessionStore = store
		s.jobIdem = &boltJobIdem{store: store}
		return nil
	}
	log.Printf("warning: session_store is not configured; paid-job idempotency records are " +
		"in-process only and do not survive restart — configure session_store for spec conformance")
	s.jobIdem = newMemJobIdem()
	return nil
}
