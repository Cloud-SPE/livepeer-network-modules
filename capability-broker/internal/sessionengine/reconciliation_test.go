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
)

func TestRemainingRevisionReservation(t *testing.T) {
	cases := []struct {
		name                          string
		price, cap, billed, requested string
		per, max, used                uint64
		want                          string
	}{
		{"remaining debit", "1000000000000", "180000000000000", "74000000000000", "120000000000000", 1, 180, 74, "106000000000000"},
		{"unit ceiling with loose debit cap", "10", "10000", "740", "1200", 1, 180, 74, "1060"},
		{"fractional cumulative ceiling", "1", "2", "1", "120", 3, 6, 1, "1"},
		{"large integer wei", "1000000000000000000000000000000", "180000000000000000000000000000000", "74000000000000000000000000000000", "120000000000000000000000000000000", 1, 180, 74, "106000000000000000000000000000000"},
		{"already paid remaining units", "1", "1", "1", "5", 3, 3, 1, "0"},
		{"billing exceeds debit", "1", "1", "2", "5", 3, 9, 4, ""},
		{"exhausted units", "1", "100", "10", "5", 1, 10, 10, ""},
		{"default reservation", "1", "180", "74", "0", 1, 180, 74, "106"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			price, _ := new(big.Int).SetString(c.price, 10)
			max, _ := new(big.Int).SetString(c.cap, 10)
			billed, _ := new(big.Int).SetString(c.billed, 10)
			requested, _ := new(big.Int).SetString(c.requested, 10)
			p := &pb.SpendAuthorizationPayload{MaxDebitWei: &pb.BigUInt{Value: max.Bytes()}, MaxTotalUnits: c.max, AcceptedPrice: &pb.AcceptedPrice{PricePerUnitWei: &pb.BigUInt{Value: price.Bytes()}, UnitsPerPrice: c.per}}
			got, err := remainingRevisionReservation(p, requested, &payment.SpendAuthorizationStatus{Billed: billed, ActualUnits: c.used})
			if c.want == "" {
				if err == nil {
					t.Fatal("exhausted revision accepted")
				}
				return
			}
			if err != nil || got.String() != c.want {
				t.Fatalf("got %v err=%v want=%s", got, err, c.want)
			}
		})
	}
}

type uncertainRevision struct {
	*fakePayment
	attempts int
}

func (p *uncertainRevision) AdmitAuthorization(ctx context.Context, req payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	p.attempts++
	return nil, errors.New("receiver unreachable")
}

func TestRevisionBackoffSurvivesRestartAndDoesNotHideUncertainty(t *testing.T) {
	h := newHarness(t)
	opened := h.open(t)
	remote := &uncertainRevision{fakePayment: h.pay}
	h.engine.cfg.Payment = remote
	if _, err := h.engine.ReviseAuthorization(context.Background(), opened.SessionID, "rev-1", revisionWire(t, h), nil, big.NewInt(100)); err == nil {
		t.Fatal("expected ambiguous failure")
	}
	rec, err := h.store.Get(opened.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.RevisionIntent == nil || !rec.RevisionIntent.NextRetryAt.After(h.now()) {
		t.Fatal("retry schedule missing")
	}
	retry := rec.RevisionIntent.NextRetryAt
	restartRevisionHarness(t, h)
	h.engine.Recover(context.Background())
	if remote.attempts != 1 {
		t.Fatal("restart bypassed retry schedule")
	}
	h.advance(retry.Sub(h.now()))
	h.engine.Recover(context.Background())
	if remote.attempts != 2 {
		t.Fatal("scheduled retry did not run")
	}
	rec, _ = h.store.Get(opened.SessionID)
	if rec.RevisionIntent == nil || rec.PaymentClosed || rec.Terminal() {
		t.Fatal("uncertain authority discarded")
	}
	// An explicit retry is still the same operation, even during cooldown.
	if _, err := h.engine.ReviseAuthorization(context.Background(), opened.SessionID, "rev-1", revisionWire(t, h), nil, big.NewInt(100)); err == nil {
		t.Fatal("uncertain retry accepted")
	}
	if remote.attempts != 3 {
		t.Fatal("explicit retry did not run")
	}
}

func TestRefusedRevisionReplayIsDurable(t *testing.T) {
	h := newHarness(t)
	opened := h.open(t)
	h.engine.cfg.Payment = &stoppedAdmission{h.pay}
	wire := revisionWire(t, h)
	_, err := h.engine.ReviseAuthorization(context.Background(), opened.SessionID, "rev-1", wire, nil, big.NewInt(100))
	var protocol *ProtocolError
	if !errors.As(err, &protocol) || protocol.Code != "refill_refused" {
		t.Fatalf("refusal: %v", err)
	}
	restartRevisionHarness(t, h)
	h.advance(time.Second)
	h.engine.cfg.Payment = h.pay
	_, err = h.engine.ReviseAuthorization(context.Background(), opened.SessionID, "rev-1", wire, nil, big.NewInt(100))
	if !errors.As(err, &protocol) || protocol.Code != "refill_refused" {
		t.Fatalf("refusal replay: %v", err)
	}
	record, _ := h.store.Get(opened.SessionID)
	if record.RevisionIntent != nil || record.AccountAuthorizationID != "auth-req-1" || record.State != sessionstore.StateActive {
		t.Fatal("refused replay changed authority")
	}
}
