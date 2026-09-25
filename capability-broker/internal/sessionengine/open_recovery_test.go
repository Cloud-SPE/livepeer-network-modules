package sessionengine

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
)

type uncertainOpenPayment struct {
	*fakePayment
	accepted       bool
	recoveryDown   bool
	afterAdmission func()
}

func (p *uncertainOpenPayment) AdmitAuthorization(ctx context.Context, req payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	if p.accepted {
		result, err := p.fakePayment.AdmitAuthorization(ctx, req)
		if err != nil {
			return result, err
		}
	}
	if p.afterAdmission != nil {
		p.afterAdmission()
	}
	return nil, errors.New("lost admission response")
}
func (p *uncertainOpenPayment) CancelAuthorizationAdmission(ctx context.Context, wire []byte) (*payment.CanceledAdmission, error) {
	if p.recoveryDown {
		return nil, errors.New("receiver unavailable")
	}
	return p.fakePayment.CancelAuthorizationAdmission(ctx, wire)
}
func TestInitialAdmissionIntentRecoversAcrossRestart(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "unaccepted", true: "accepted"}[accepted], func(t *testing.T) {
			h := newHarness(t)
			p := &uncertainOpenPayment{fakePayment: h.pay, accepted: accepted, recoveryDown: true}
			h.engine.cfg.Payment = p
			req := capacityOpenRequest(t, h)
			if _, err := h.engine.Open(context.Background(), req); err == nil {
				t.Fatal("expected uncertain open")
			}
			held, err := h.store.Reservation(req.RequestID)
			if err != nil || held.AdmissionIntent == nil || string(held.AdmissionIntent.AuthorizationBytes) != string(req.AuthorizationBytes) || held.Stage != sessionstore.ReservationReserved {
				t.Fatalf("lost intent: %+v %v", held, err)
			}
			if h.runner.created != 0 || len(h.release) != 1 {
				t.Fatal("unexpected runner/capacity effects")
			}
			restartRevisionHarness(t, h)
			p.recoveryDown = false
			h.engine.Recover(context.Background())
			if accepted {
				id, err := h.store.SessionIDForRequest(req.RequestID)
				if err != nil {
					t.Fatal(err)
				}
				rec, err := h.store.Get(id)
				if err != nil || !rec.Terminal() || !rec.PaymentClosed || rec.SettlementSeq == 0 {
					t.Fatalf("unsettled recovery: %+v %v", rec, err)
				}
			} else {
				held, err = h.store.Reservation(req.RequestID)
				if err != nil || !held.AdmissionCanceled {
					t.Fatalf("unfenced: %+v %v", held, err)
				}
			}
			if _, err := h.engine.Open(context.Background(), req); err == nil {
				t.Fatal("replayed failed open")
			}
			h.engine.Sweep(context.Background())
			if h.runner.created != 0 || len(h.release) != 1 {
				t.Fatal("duplicate effects after restart")
			}
		})
	}
}

// Accepted payment plus a failed local success write must leave the pre-RPC
// intent sufficient to recover, without retrying payment or creating a runner.
type successWriteCrashPayment struct {
	*fakePayment
	crash func()
}

func (p successWriteCrashPayment) AdmitAuthorization(ctx context.Context, r payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	result, err := p.fakePayment.AdmitAuthorization(ctx, r)
	p.crash()
	return result, err
}
func TestInitialAdmissionAcceptedBeforeLocalSuccessWrite(t *testing.T) {
	h := newHarness(t)
	h.engine.cfg.Payment = successWriteCrashPayment{h.pay, func() { _ = h.store.Close() }}
	if _, err := h.engine.Open(context.Background(), capacityOpenRequest(t, h)); err == nil {
		t.Fatal("missing write failure")
	}
	h.engine.cfg.Payment = h.pay
	restartRevisionHarness(t, h)
	h.engine.Recover(context.Background())
	id, err := h.store.SessionIDForRequest("req-1")
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := h.store.Get(id)
	if !rec.Terminal() || !rec.PaymentClosed || h.pay.openCalls != 1 || h.runner.created != 0 || len(h.release) != 1 {
		t.Fatalf("unsafe recovery: %+v", rec)
	}
}

type recoveringRunner struct {
	*fakeRunner
	outcome     string
	unavailable bool
}

func (r *recoveringRunner) ReconcileSessionCreate(_ context.Context, id string) (*RunnerCreateResolution, error) {
	if r.unavailable {
		return nil, errors.New("runner offline")
	}
	result := &RunnerCreateResolution{SessionID: id, Outcome: r.outcome}
	if r.outcome == "created" {
		result.RunnerSessionID = "rns-lost"
	}
	return result, nil
}
func TestLostCreateResponseReconcilesAfterRestart(t *testing.T) {
	for _, outcome := range []string{"created", "fenced"} {
		t.Run(outcome, func(t *testing.T) {
			h := newHarness(t)
			h.runner.failCreate = true
			runner := &recoveringRunner{fakeRunner: h.runner, outcome: outcome, unavailable: true}
			h.engine.cfg.Runner = func(string) RunnerClient { return runner }
			if _, err := h.engine.Open(context.Background(), capacityOpenRequest(t, h)); err == nil {
				t.Fatal("expected lost create")
			}
			if len(h.release) != 0 {
				t.Fatal("released uncertain runner")
			}
			restartRevisionHarness(t, h)
			runner.unavailable = false
			h.engine.Recover(context.Background())
			id, err := h.store.SessionIDForRequest("req-1")
			if err != nil {
				t.Fatal(err)
			}
			rec, _ := h.store.Get(id)
			if !rec.Terminal() || !rec.PaymentClosed || len(h.release) != 1 {
				t.Fatalf("recovery=%+v release=%v", rec, h.release)
			}
			count := 0
			if outcome == "created" {
				count = 1
			}
			if len(h.runner.terminated) != count {
				t.Fatalf("terminations=%v", h.runner.terminated)
			}
			restartRevisionHarness(t, h)
			h.engine.Recover(context.Background())
			if len(h.release) != 1 || len(h.runner.terminated) != count {
				t.Fatal("duplicate cleanup")
			}
		})
	}
}

func TestLegacyReservedAdmissionCannotClaimNonAdmission(t *testing.T) {
	h := newHarness(t)
	if err := h.store.ReserveOpen("old-open", []byte("fp")); err != nil {
		t.Fatal(err)
	}
	if err := h.store.UpdateReservation("old-open", func(r *sessionstore.OpenReservation) error {
		r.AdmissionTracked = false
		r.CapacityTracked = true
		r.CapacityRef = "old-slot"
		r.BackendRef = "b1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.engine.Recover(context.Background())
	held, err := h.store.Reservation("old-open")
	if err != nil || held.AdmissionCanceled || !held.RunnerTerminated || held.CapacityRef != "" || len(h.release) != 1 || len(h.pay.accountSettles) != 0 {
		t.Fatalf("legacy uncertainty discarded: %+v %v", held, err)
	}
}

func TestInitialRecoveryPreservesSignedPriceWithoutLiveOffer(t *testing.T) {
	for _, removed := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed", true: "removed"}[removed], func(t *testing.T) {
			h := newHarness(t)
			p := &uncertainOpenPayment{fakePayment: h.pay, accepted: true, recoveryDown: true}
			h.engine.cfg.Payment = p
			if _, err := h.engine.Open(context.Background(), capacityOpenRequest(t, h)); err == nil {
				t.Fatal("expected lost reply")
			}
			restartRevisionHarness(t, h)
			h.engine.cfg.Specs = func(string) *OfferingSpec {
				if removed {
					return nil
				}
				return &OfferingSpec{PricePerWorkUnitWei: big.NewInt(999), PerUnits: 3}
			}
			p.recoveryDown = false
			h.engine.Recover(context.Background())
			id, err := h.store.SessionIDForRequest("req-1")
			if err != nil {
				t.Fatal(err)
			}
			record, err := h.engine.RecordSettlement(context.Background(), id)
			if err != nil || record.GetState() != "closed" || record.GetPerUnits() != 1 || new(big.Int).SetBytes(record.GetAmountWei().GetValue()).Int64() != 10 {
				t.Fatalf("changed price in terminal evidence: %v %v", record, err)
			}
		})
	}
}
