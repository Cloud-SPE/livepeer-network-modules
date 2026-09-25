package sessionengine

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type refusedThenLostFence struct {
	*fakePayment
	attempts, fences int
}

func (p *refusedThenLostFence) AdmitAuthorization(context.Context, payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	p.attempts++
	s, _ := status.New(codes.FailedPrecondition, "do not persist this upstream text").WithDetails(&errdetails.ErrorInfo{Domain: "payments.livepeer.org", Reason: "INSUFFICIENT_WHOLESALE_CREDIT"})
	return nil, s.Err()
}
func (p *refusedThenLostFence) CancelAuthorizationAdmission(ctx context.Context, wire []byte) (*payment.CanceledAdmission, error) {
	p.fences++
	result, err := p.fakePayment.CancelAuthorizationAdmission(ctx, wire)
	if p.fences == 1 {
		return nil, status.Error(codes.Unavailable, "lost fence response")
	}
	return result, err
}
func TestRefusalCauseSurvivesLostCancellationAndRestart(t *testing.T) {
	h := newHarness(t)
	opened := h.open(t)
	remote := &refusedThenLostFence{fakePayment: h.pay}
	h.engine.cfg.Payment = remote
	wire := revisionWire(t, h)
	_, err := h.engine.ReviseAuthorization(context.Background(), opened.SessionID, "refill", wire, nil, big.NewInt(100))
	var pending *RetryableError
	if !errors.As(err, &pending) || pending.Revision == nil || pending.Revision.Outcome != "pending" || pending.Revision.Reason != "INSUFFICIENT_WHOLESALE_CREDIT" {
		t.Fatalf("pending: %v", err)
	}
	restartRevisionHarness(t, h)
	h.advance(2 * time.Second)
	h.engine.Recover(context.Background())
	_, err = h.engine.ReviseAuthorization(context.Background(), opened.SessionID, "refill", wire, nil, big.NewInt(100))
	var refused *ProtocolError
	if !errors.As(err, &refused) || refused.Revision == nil || refused.Revision.Reason != pending.Revision.Reason || refused.Revision.Outcome != "refused" {
		t.Fatalf("refusal: %v", err)
	}
	if remote.attempts != 1 || remote.fences != 2 {
		t.Fatalf("restarted admission after cancellation: %d/%d", remote.attempts, remote.fences)
	}
	result, err := h.store.GetRevision(opened.SessionID, "refill")
	if err != nil || len(result.RevisionEvidence) == 0 {
		t.Fatal("final proof missing", err)
	}
	// Outcome eviction must never make the fenced identity a fresh refill.
	if _, err := h.store.EvictTopUps(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, err = h.engine.ReviseAuthorization(context.Background(), opened.SessionID, "refill", wire, nil, big.NewInt(100))
	if !errors.As(err, &refused) || refused.Code != "request_id_reuse" || remote.attempts != 1 {
		t.Fatalf("evicted outcome was readmitted: %v", err)
	}
}
