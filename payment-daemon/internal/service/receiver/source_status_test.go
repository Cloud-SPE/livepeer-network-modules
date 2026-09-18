package receiver

import (
	"context"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/types"
	"math/big"
	"testing"
	"time"
)

func TestSourceRetirementWaitsForFinalRedemptionAndConfirmedRound(t *testing.T) {
	svc, st, _, payee := receiverFixture(t)
	ctx := context.Background()
	round := int64(99)
	svc.RevenueReporter = func(_ context.Context, r int64) (*pb.GetRoundRevenueResponse, error) {
		return &pb.GetRoundRevenueResponse{RoundId: r, Complete: true, CompleteThroughRound: round, ObservedAt: time.Now().Format(time.RFC3339Nano)}, nil
	}
	hash := bytesOf(1, 32)
	ticket := &store.SignedTicket{Sender: bytesOf(2, 20), Recipient: payee, FaceValue: big.NewInt(10), CreationRound: 98}
	if _, err := st.EnqueueRedemption(hash, ticket); err != nil {
		t.Fatal(err)
	}
	req := &pb.GetRevenueSourceStatusRequest{SettlementDomainId: svc.settlementDomainID}
	status, err := svc.FreezeRevenueSource(ctx, &pb.FreezeRevenueSourceRequest{SettlementDomainId: svc.settlementDomainID, Reason: "retire settled source"})
	if err != nil || !status.Frozen || status.Complete || status.PendingRedemptions != 1 {
		t.Fatalf("queued source %+v %v", status, err)
	}
	evidence := &types.RedemptionInclusion{TxHash: bytesOf(3, 32), BlockHash: bytesOf(4, 32), BlockNumber: 1000, Round: 100}
	if err := st.MarkRedeemedWithInclusion(hash, ticket, evidence); err != nil {
		t.Fatal(err)
	}
	status, err = svc.GetRevenueSourceStatus(ctx, req)
	if err != nil || status.Complete || status.LastRedemptionRound != 100 {
		t.Fatalf("uncovered inclusion %+v %v", status, err)
	}
	round = 100
	status, err = svc.GetRevenueSourceStatus(ctx, req)
	if err != nil || !status.Complete || status.PendingRedemptions != 0 || status.CompleteThroughRound != 100 {
		t.Fatalf("settled source %+v %v", status, err)
	}
	// A freshly constructed service over the restored ledger retains intake fencing.
	restarted := New(st, Config{SettlementDomainID: svc.settlementDomainID, Recipient: payee, ChainID: 42161}, nil)
	if _, err = restarted.GetTicketParams(ctx, &pb.GetTicketParamsRequest{}); err == nil {
		t.Fatal("restart resumed frozen intake")
	}
}
