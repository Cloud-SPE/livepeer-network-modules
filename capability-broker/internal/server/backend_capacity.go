package server

import (
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
)

func (s *Server) markBackendCapacityRefused(backendID string, backoffSeconds int) {
	if s == nil || backendID == "" {
		return
	}
	if backoffSeconds <= 0 || backoffSeconds > maxCapacityBackoffSeconds {
		backoffSeconds = defaultCapacityBackoffSeconds
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.backendCapacityUntil == nil {
		s.backendCapacityUntil = make(map[string]time.Time)
	}
	until := time.Now().Add(time.Duration(backoffSeconds) * time.Second)
	if until.After(s.backendCapacityUntil[backendID]) {
		s.backendCapacityUntil[backendID] = until
	}
}

func (s *Server) backendCapacityBackoff(backendID string, now time.Time) bool {
	if s == nil || backendID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	until := s.backendCapacityUntil[backendID]
	if until.IsZero() {
		return false
	}
	if !now.Before(until) {
		delete(s.backendCapacityUntil, backendID)
		return false
	}
	return true
}

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
