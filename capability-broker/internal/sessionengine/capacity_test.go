package sessionengine

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
)

func capacityOpenRequest(t *testing.T, h *harness) OpenRequest {
	return OpenRequest{RequestID: "req-1", GatewaySessionID: "gws-1", SessionParams: json.RawMessage(`{"room_hint":"standup"}`), PaymentBytes: []byte{1, 2, 3}, AuthorizationBytes: h.authorization(t, "auth-req-1", "req-1", "gws-1", 100), InitialReservationWei: big.NewInt(50), Spec: h.spec, CapacityRef: "cap-slot-1"}
}

func TestCapacityAcquiredOnceAcrossReplayAndRevision(t *testing.T) {
	h := newHarness(t)
	acquired := 0
	h.engine.cfg.AcquireCapacity = func(ref string, spec *OfferingSpec) error { acquired++; return nil }
	req := capacityOpenRequest(t, h)
	res, err := h.engine.Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.engine.Open(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err = h.engine.ReviseAuthorization(context.Background(), res.SessionID, "rev-1", revisionWire(t, h), nil, big.NewInt(100)); err != nil {
		t.Fatal(err)
	}
	if acquired != 1 {
		t.Fatalf("acquired %d slots", acquired)
	}
	if _, err = h.engine.End(context.Background(), res.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	h.engine.End(context.Background(), res.SessionID, "")
	if len(h.release) != 1 || h.release[0] != res.SessionID {
		t.Fatalf("releases=%v", h.release)
	}
}

func TestCapacityRejectionPrecedesPaymentAndCreate(t *testing.T) {
	h := newHarness(t)
	h.engine.cfg.AcquireCapacity = func(string, *OfferingSpec) error { return errors.New("full") }
	_, err := h.engine.Open(context.Background(), capacityOpenRequest(t, h))
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "capacity_exhausted" {
		t.Fatalf("err=%v", err)
	}
	if h.pay.openCalls != 0 || h.runner.created != 0 || len(h.release) != 0 {
		t.Fatalf("admission side effects: %d %d %v", h.pay.openCalls, h.runner.created, h.release)
	}
	if _, err = h.store.Reservation("req-1"); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Fatalf("reservation=%v", err)
	}
}

func TestStoppedSessionReleasesCapacityBeforeSettlementAcrossRestart(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	h.pay.failSettles = 1
	if _, err := h.engine.End(context.Background(), res.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	rec, _ := h.store.Get(res.SessionID)
	if rec.Terminal() || !rec.RunnerTerminated || rec.PaymentClosed || rec.CapacityRef != "" || len(h.release) != 1 {
		t.Fatalf("pending settlement record=%+v releases=%v", rec, h.release)
	}
	restartRevisionHarness(t, h)
	h.engine.Recover(context.Background())
	rec, _ = h.store.Get(res.SessionID)
	if !rec.Terminal() || !rec.PaymentClosed || len(h.release) != 1 || len(h.runner.terminated) != 1 {
		t.Fatalf("recovery record=%+v releases=%v terminations=%v", rec, h.release, h.runner.terminated)
	}
}

func TestBadDescriptorRetainsCapacityUntilCleanupConfirmed(t *testing.T) {
	h := newHarness(t)
	h.runner.runtime = json.RawMessage(`{"schema":"sfu-room/v1","public":{},"surprise":{}}`)
	h.runner.failTerminate = true
	if _, err := h.engine.Open(context.Background(), capacityOpenRequest(t, h)); err == nil {
		t.Fatal("expected failed open")
	}
	r, err := h.store.Reservation("req-1")
	if err != nil || r.RunnerSessionID == "" || r.CapacityRef == "" || len(h.release) != 0 || len(h.pay.accountSettles) != 0 {
		t.Fatalf("uncertain cleanup: %+v %v releases=%v", r, err, h.release)
	}
	restartRevisionHarness(t, h)
	h.engine.Recover(context.Background())
	if len(h.release) != 0 {
		t.Fatal("released unreachable runner")
	}
	h.runner.failTerminate = false
	h.pay.failSettles = 1
	h.engine.Sweep(context.Background())
	r, err = h.store.Reservation("req-1")
	if err != nil || !r.RunnerTerminated || r.CapacityRef != "" || len(h.release) != 1 {
		t.Fatalf("stopped but payment pending: %+v %v releases=%v", r, err, h.release)
	}
	h.engine.Sweep(context.Background())
	if _, err = h.store.Reservation("req-1"); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Fatal(err)
	}
	if len(h.release) != 1 || len(h.runner.terminated) != 1 {
		t.Fatalf("duplicate cleanup releases=%v termination=%v", h.release, h.runner.terminated)
	}
}

func TestLostCreateResponseRetainsOwnershipAcrossRestart(t *testing.T) {
	h := newHarness(t)
	h.runner.failCreate = true
	if _, err := h.engine.Open(context.Background(), capacityOpenRequest(t, h)); err == nil {
		t.Fatal("expected lost create response")
	}
	restartRevisionHarness(t, h)
	h.engine.Recover(context.Background())
	h.engine.Sweep(context.Background())
	r, err := h.store.Reservation("req-1")
	if err != nil || !r.CreateStarted || r.BrokerSessionID == "" || r.CapacityRef == "" || len(h.release) != 0 || len(h.pay.accountSettles) != 0 {
		t.Fatalf("uncertain create: %+v %v release=%v settlements=%v", r, err, h.release, h.pay.accountSettles)
	}
	h.runner.failCreate = false
	if _, err = h.engine.Open(context.Background(), capacityOpenRequest(t, h)); err == nil {
		t.Fatal("retried unresolved create")
	}
	if h.runner.created != 0 {
		t.Fatal("replayed a possibly accepted create")
	}
}

type capacityAdmissionFailure struct{ *fakePayment }

func (p capacityAdmissionFailure) AdmitAuthorization(context.Context, payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	return nil, errors.New("admission rejected")
}

func TestFailedAdmissionReleasesCapacityWithoutRunnerEffects(t *testing.T) {
	h := newHarness(t)
	h.engine.cfg.Payment = capacityAdmissionFailure{h.pay}
	if _, err := h.engine.Open(context.Background(), capacityOpenRequest(t, h)); err == nil {
		t.Fatal("expected failed admission")
	}
	if len(h.release) != 1 || h.runner.created != 0 {
		t.Fatalf("release=%v creates=%d", h.release, h.runner.created)
	}
	if _, err := h.store.Reservation("req-1"); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestLegacyPaidOpenCannotProveCreateDidNotRun(t *testing.T) {
	h := newHarness(t)
	if err := h.store.ReserveOpen("legacy", []byte("fp")); err != nil {
		t.Fatal(err)
	}
	if err := h.store.UpdateReservation("legacy", func(r *sessionstore.OpenReservation) error {
		r.Stage = sessionstore.ReservationPaid
		r.WorkID = "legacy-auth"
		r.AccountAuthorization = true
		r.BackendRef = "b1"
		r.CapacityRef = "legacy-slot"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	restartRevisionHarness(t, h)
	h.engine.Recover(context.Background())
	if _, err := h.store.Reservation("legacy"); err != nil {
		t.Fatal(err)
	}
	if len(h.release) != 0 || len(h.pay.accountSettles) != 0 {
		t.Fatal("legacy create ambiguity was treated as absence")
	}
}
