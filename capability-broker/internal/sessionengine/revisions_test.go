package sessionengine

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

type revisionPayment struct {
	*fakePayment
	accepted, unavailable bool
	mutations, calls      int
}

func (p *revisionPayment) AdmitAuthorization(ctx context.Context, req payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	var auth pb.SpendAuthorization
	if err := proto.Unmarshal(req.AuthorizationBytes, &auth); err != nil {
		return nil, err
	}
	if auth.GetPayload().GetPredecessorAuthorizationId() != "" {
		p.calls++
		if !p.accepted {
			p.accepted = true
			p.mutations++
		}
		result, err := p.fakePayment.AdmitAuthorization(ctx, req)
		if p.unavailable {
			return nil, errors.New("accepted but response lost")
		}
		return result, err
	}
	return p.fakePayment.AdmitAuthorization(ctx, req)
}

func (p *revisionPayment) GetSpendAuthorization(ctx context.Context, payer []byte, id string) (*payment.SpendAuthorizationStatus, error) {
	if p.accepted && id == "auth-req-1" {
		return &payment.SpendAuthorizationStatus{State: int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SUPERSEDED)}, nil
	}
	return p.fakePayment.GetSpendAuthorization(ctx, payer, id)
}

func revisionWire(t *testing.T, h *harness) []byte {
	t.Helper()
	var auth pb.SpendAuthorization
	if err := proto.Unmarshal(h.authorization(t, "auth-revised", "rev-1", "gws-1", 200), &auth); err != nil {
		t.Fatal(err)
	}
	auth.Payload.PredecessorAuthorizationId = "auth-req-1"
	auth.Payload.Revision = 1
	auth.Payload.MaxDebitWei = &pb.BigUInt{Value: big.NewInt(2000).Bytes()}
	wire, err := proto.Marshal(&auth)
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func restartRevisionHarness(t *testing.T, h *harness) {
	t.Helper()
	if err := h.store.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := sessionstore.Open(h.storePath, make([]byte, sessionstore.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := h.engine.cfg
	cfg.Store = st
	h.engine, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.store = st
}

func TestRevisionLostAdmissionResponseRestartsWithoutSettlingPredecessor(t *testing.T) {
	h := newHarness(t)
	p := &revisionPayment{fakePayment: h.pay, unavailable: true}
	h.engine.cfg.Payment = p
	session := h.open(t)
	wire := revisionWire(t, h)
	ctx := context.Background()
	if _, err := h.engine.ReviseAuthorization(ctx, session.SessionID, "rev-1", wire, nil, big.NewInt(100)); err == nil {
		t.Fatal("expected lost response")
	}
	rec, err := h.store.Get(session.SessionID)
	if err != nil || rec.RevisionIntent == nil || rec.AccountAuthorizationID != "auth-req-1" {
		t.Fatalf("intent not persisted: %+v %v", rec, err)
	}
	lease := rec.RevisionIntent.LeaseExpiresAt
	if _, err := h.engine.ReviseAuthorization(ctx, session.SessionID, "different", wire, nil, big.NewInt(100)); err == nil {
		t.Fatal("accepted another pending revision")
	}
	if _, err := h.engine.ReviseAuthorization(ctx, session.SessionID, "rev-1", wire, []byte("different"), big.NewInt(100)); err == nil {
		t.Fatal("accepted changed request")
	}
	if _, err := h.engine.ProcessEvent(ctx, session.SessionID, usageEvent("tick", 1, 3)); err == nil {
		t.Fatal("usage billed under unresolved authority")
	}
	if len(p.accountAdvances) != 0 {
		t.Fatal("advanced superseded authorization")
	}
	restartRevisionHarness(t, h)
	h.engine.Recover(ctx)
	rec, _ = h.store.Get(session.SessionID)
	if rec.PaymentClosed || rec.Terminal() || len(p.accountSettles) != 0 {
		t.Fatalf("unresolved revision discarded: %+v", rec)
	}
	p.unavailable = false
	h.advance(2 * time.Second)
	h.engine.Recover(ctx)
	rec, _ = h.store.Get(session.SessionID)
	if rec.RevisionIntent != nil || rec.AccountAuthorizationID != "auth-revised" || rec.PaymentClosed || rec.Terminal() {
		t.Fatalf("recovery: %+v", rec)
	}
	calls := p.calls
	restartRevisionHarness(t, h)
	reply, err := h.engine.ReviseAuthorization(ctx, session.SessionID, "rev-1", wire, nil, big.NewInt(100))
	if err != nil || !reply.Lease.Equal(lease) || reply.Balance.String() != "1000" || p.calls != calls || p.mutations != 1 {
		t.Fatalf("replay: %+v %v calls=%d mutations=%d", reply, err, p.calls, p.mutations)
	}
}

func TestRevisionWaitsForPendingUsageDebit(t *testing.T) {
	h := newHarness(t)
	session := h.open(t)
	if err := h.store.Update(session.SessionID, func(r *sessionstore.Record) error { r.PendingDebitSeq = 1; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.ReviseAuthorization(context.Background(), session.SessionID, "rev-1", revisionWire(t, h), nil, big.NewInt(100)); err == nil {
		t.Fatal("revision overtook pending usage")
	}
	rec, _ := h.store.Get(session.SessionID)
	if rec.RevisionIntent != nil || h.pay.openCalls != 1 {
		t.Fatal("revision sent before prior usage resolved")
	}
}

type stoppedAdmission struct{ *fakePayment }

func (s *stoppedAdmission) AdmitAuthorization(context.Context, payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	return nil, payment.ErrAdmissionStopped
}
func TestRevisionLocalFenceRefusalDoesNotLeaveUnresolvableIntent(t *testing.T) {
	h := newHarness(t)
	session := h.open(t)
	h.engine.cfg.Payment = &stoppedAdmission{h.pay}
	if _, err := h.engine.ReviseAuthorization(context.Background(), session.SessionID, "rev-1", revisionWire(t, h), nil, big.NewInt(100)); err == nil {
		t.Fatal("paused admission accepted")
	}
	record, err := h.store.Get(session.SessionID)
	if err != nil || record.RevisionIntent != nil || record.AccountAuthorizationID != "auth-req-1" {
		t.Fatalf("local refusal retained financial uncertainty %+v %v", record, err)
	}
}
