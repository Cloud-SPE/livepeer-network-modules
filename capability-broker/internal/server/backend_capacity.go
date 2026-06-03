package server

import "github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"

func (s *Server) currentBackendInFlight(backendID string) int {
	if s == nil || backendID == "" {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.backendInFlight[backendID]
}

func (s *Server) reserveBackend(cap *config.Capability) (func(), bool) {
	if s == nil || cap == nil {
		return func() {}, true
	}
	backendID := backendIDForCapability(cap)
	if backendID == "" {
		return func() {}, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.backendInFlight == nil {
		s.backendInFlight = make(map[string]int)
	}
	if cap.Backend.MaxInFlight > 0 && s.backendInFlight[backendID] >= cap.Backend.MaxInFlight {
		return func() {}, false
	}
	s.backendInFlight[backendID]++
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.backendInFlight[backendID] <= 1 {
			delete(s.backendInFlight, backendID)
			return
		}
		s.backendInFlight[backendID]--
	}, true
}
