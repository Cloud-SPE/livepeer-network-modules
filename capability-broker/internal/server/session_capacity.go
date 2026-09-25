package server

import (
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionengine"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
)

func capacityBackend(ref string) string {
	_, _, pair, pinned := splitSessionBackendRef(ref)
	if pinned {
		return pair.HostID + "|" + pair.LocalID
	}
	return ref
}

func (s *Server) acquireSessionCapacity(ref string, spec *sessionengine.OfferingSpec) error {
	capID, offID, pair, pinned := splitSessionBackendRef(spec.BackendRef)
	if !pinned {
		return fmt.Errorf("session runner is not pinned")
	}
	cap := s.pinnedSessionCapability(capID, offID, pair)
	if cap == nil {
		return fmt.Errorf("session offering unavailable")
	}
	if err := s.acquireSessionRunner(ref, cap); err == nil {
		return nil
	}
	// Another open may have taken the initially selected slot. The offer's
	// frozen shape is identical on every eligible runner; only its pin changes.
	group, ok := s.offerGroupFor(capID, offID)
	if !ok {
		return fmt.Errorf("session offering unavailable")
	}
	remaining := *group
	remaining.Backends = append([]*config.Capability(nil), group.Backends...)
	for len(remaining.Backends) > 0 {
		next, err := s.selectRunnerBackend(&remaining)
		if err != nil {
			return err
		}
		if err = s.acquireSessionRunner(ref, next); err == nil {
			spec.BackendRef = specFromCapability(next).BackendRef
			return nil
		}
		for i, candidate := range remaining.Backends {
			if backendIDForCapability(candidate) == backendIDForCapability(next) {
				remaining.Backends = append(remaining.Backends[:i], remaining.Backends[i+1:]...)
				break
			}
		}
	}
	return fmt.Errorf("no runner capacity available")
}

func (s *Server) acquireSessionRunner(ref string, cap *config.Capability) error {
	backendID := backendIDForCapability(cap)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.capacityOwners == nil {
		s.capacityOwners = make(map[string]string)
	}
	if previous, ok := s.capacityOwners[ref]; ok {
		if previous != backendID {
			return fmt.Errorf("capacity owner changed runner")
		}
		return nil
	}
	if cap.Backend.MaxInFlight > 0 && s.backendInFlight[backendID] >= cap.Backend.MaxInFlight {
		return fmt.Errorf("runner at capacity")
	}
	if s.backendInFlight == nil {
		s.backendInFlight = make(map[string]int)
	}
	s.capacityOwners[ref] = backendID
	s.backendInFlight[backendID]++
	return nil
}

func (s *Server) releaseSessionCapacity(ref string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	backendID, ok := s.capacityOwners[ref]
	if !ok {
		return
	}
	delete(s.capacityOwners, ref)
	if s.backendInFlight[backendID] <= 1 {
		delete(s.backendInFlight, backendID)
	} else {
		s.backendInFlight[backendID]--
	}
}

// Runs before serving, without consulting runner connectivity or current limits.
// An existing workload still occupies capacity when a limit is reduced.
func (s *Server) restoreSessionCapacity(store *sessionstore.Store) error {
	owners := make(map[string]string)
	var records []*sessionstore.Record
	if err := store.ForEach(func(r *sessionstore.Record) error { records = append(records, r); return nil }); err != nil {
		return err
	}
	for _, r := range records {
		if r.Terminal() || r.RunnerTerminated {
			continue
		}
		if r.CapacityRef == "" {
			r.CapacityRef = "session:" + r.SessionID
			if err := store.Update(r.SessionID, func(current *sessionstore.Record) error { current.CapacityRef = r.CapacityRef; return nil }); err != nil {
				return err
			}
		}
		if _, _, _, pinned := splitSessionBackendRef(r.BackendRef); !pinned {
			return fmt.Errorf("live session %s missing pinned runner binding", r.SessionID)
		}
		if _, exists := owners[r.CapacityRef]; exists {
			return fmt.Errorf("duplicate session capacity owner %q", r.CapacityRef)
		}
		owners[r.CapacityRef] = capacityBackend(r.BackendRef)
	}
	var opens []sessionstore.OpenReservation
	if err := store.ForEachReservation(func(r sessionstore.OpenReservation) error { opens = append(opens, r); return nil }); err != nil {
		return err
	}
	for _, r := range opens {
		if r.RunnerTerminated {
			continue
		}
		if r.BackendRef == "" && r.Stage == sessionstore.ReservationReserved {
			continue
		}
		if _, _, _, pinned := splitSessionBackendRef(r.BackendRef); !pinned {
			return fmt.Errorf("opening request %s missing pinned runner binding", r.RequestID)
		}
		if r.CapacityRef == "" {
			r.CapacityRef = "open:" + r.RequestID
		}
		if err := store.UpdateReservation(r.RequestID, func(current *sessionstore.OpenReservation) error { current.CapacityRef = r.CapacityRef; return nil }); err != nil {
			return err
		}
		if _, exists := owners[r.CapacityRef]; exists {
			return fmt.Errorf("duplicate session capacity owner %q", r.CapacityRef)
		}
		owners[r.CapacityRef] = capacityBackend(r.BackendRef)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.backendInFlight == nil {
		s.backendInFlight = make(map[string]int)
	}
	for _, backendID := range s.capacityOwners {
		if s.backendInFlight[backendID] <= 1 {
			delete(s.backendInFlight, backendID)
		} else {
			s.backendInFlight[backendID]--
		}
	}
	s.capacityOwners = owners
	for _, backendID := range owners {
		s.backendInFlight[backendID]++
	}
	return nil
}
