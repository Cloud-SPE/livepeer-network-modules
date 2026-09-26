package sessionengine

import (
	"context"
	"errors"
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
)

// Persist proof of resource release before advertising a free slot. Financial
// reconciliation may continue on the winding-down record without occupying GPU.
func (e *Engine) releaseStoppedCapacity(rec *sessionstore.Record, reason string) error {
	ref := rec.CapacityRef
	if rec.RunnerTerminated && ref == "" && rec.State == sessionstore.StateWindingDown {
		return nil
	}
	if err := e.cfg.Store.Update(rec.SessionID, func(r *sessionstore.Record) error {
		r.State, r.CloseReason, r.ReplayMaterial = sessionstore.StateWindingDown, reason, nil
		r.RunnerTerminated = true
		r.CapacityRef = ""
		return nil
	}); err != nil {
		return err
	}
	rec.RunnerTerminated, rec.CapacityRef = true, ""
	rec.State, rec.CloseReason = sessionstore.StateWindingDown, reason
	e.release(ref)
	return nil
}

// Caller holds the per-request open mutex. A failed cleanup retains durable
// obligations. In particular a missing create response is not proof of absence.
func (e *Engine) cleanupOpenReservation(ctx context.Context, id string) error {
	r, err := e.cfg.Store.Reservation(id)
	if errors.Is(err, sessionstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.AdmissionCanceled {
		return nil
	}
	if !r.RunnerTerminated {
		creating := r.CreateStarted || r.Stage == sessionstore.ReservationRunnerCreated || (!r.CapacityTracked && r.Stage == sessionstore.ReservationPaid)
		if creating {
			if r.RunnerSessionID == "" {
				recovery, ok := e.runnerFor(r.BackendRef).(RunnerCreateRecovery)
				if !ok || r.BrokerSessionID == "" {
					return fmt.Errorf("runner create outcome unknown for broker session %s on %s; capacity retained", r.BrokerSessionID, r.BackendRef)
				}
				result, err := recovery.ReconcileSessionCreate(ctx, r.BrokerSessionID)
				if err != nil {
					return err
				}
				if result == nil || result.SessionID != r.BrokerSessionID {
					return fmt.Errorf("runner create recovery identity mismatch")
				}
				switch result.Outcome {
				case "created":
					if result.RunnerSessionID == "" {
						return fmt.Errorf("runner recovery identity missing")
					}
					if err = e.cfg.Store.UpdateReservation(id, func(current *sessionstore.OpenReservation) error {
						current.RunnerSessionID = result.RunnerSessionID
						return nil
					}); err != nil {
						return err
					}
					r.RunnerSessionID = result.RunnerSessionID
				case "fenced":
					if result.RunnerSessionID != "" {
						return fmt.Errorf("conflicting runner recovery proof")
					}
				default:
					return fmt.Errorf("runner create recovery pending")
				}
			}
			if r.RunnerSessionID != "" {
				if err := e.runnerFor(r.BackendRef).TerminateSession(ctx, r.RunnerSessionID, ReasonOpenFailed); err != nil && !errors.Is(err, ErrRunnerSessionGone) {
					return err
				}
			}
		}
		if err := e.cfg.Store.UpdateReservation(id, func(current *sessionstore.OpenReservation) error {
			current.RunnerTerminated = true
			current.CapacityRef = ""
			return nil
		}); err != nil {
			return err
		}
		e.release(r.CapacityRef)
	}
	if r.AdmissionIntent != nil {
		return e.resolveOpenAdmission(ctx, r)
	}
	if !r.AdmissionTracked || r.Stage != sessionstore.ReservationReserved {
		return fmt.Errorf("legacy initial admission lacks recovery authority; reservation retained for reconciliation")
	}

	return e.cfg.Store.ReleaseReservation(id)
}
