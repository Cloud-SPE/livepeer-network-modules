package sessionengine

import (
	"context"
	"errors"
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
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
	if !r.RunnerTerminated {
		creating := r.CreateStarted || r.Stage == sessionstore.ReservationRunnerCreated || (!r.CapacityTracked && r.Stage == sessionstore.ReservationPaid)
		if creating {
			if r.RunnerSessionID == "" {
				return fmt.Errorf("runner create outcome unknown for broker session %s on %s; capacity retained", r.BrokerSessionID, r.BackendRef)
			}
			if err := e.runnerFor(r.BackendRef).TerminateSession(ctx, r.RunnerSessionID, ReasonOpenFailed); err != nil && !errors.Is(err, ErrRunnerSessionGone) {
				return err
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
	if r.Stage == sessionstore.ReservationPaid || r.Stage == sessionstore.ReservationRunnerCreated {
		ac, ok := e.cfg.Payment.(payment.AccountClient)
		if !ok {
			return fmt.Errorf("account receiver unavailable")
		}
		settled, err := ac.SettleAuthorization(ctx, payment.SettleAuthorizationRequest{Payer: r.Sender, AuthorizationID: r.WorkID, ActualUnits: 0, SettlementSeq: 1})
		if err != nil {
			return err
		}
		if settled == nil || settled.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED) {
			return fmt.Errorf("authorization settlement unconfirmed")
		}
	}
	return e.cfg.Store.ReleaseReservation(id)
}
