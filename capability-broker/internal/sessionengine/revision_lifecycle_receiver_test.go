package sessionengine

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

func TestRealReceiverMultipleRefillsAndResponseReplay(t *testing.T) {
	client, restart := receiverForRevision(t)
	h := newHarness(t)
	h.engine.cfg.Payment = client
	h.nowVal = time.Now().UTC()
	h.spec.PricePerWorkUnitWei = big.NewInt(10)
	h.spec.MinRunwayUnits = 120
	ctx := context.Background()
	opened, err := h.engine.Open(ctx, OpenRequest{RequestID: "initial", GatewaySessionID: "gws-1", AuthorizationBytes: realRevisionWire(t, h, "initial", "", 120, false), InitialReservationWei: big.NewInt(1200), Spec: h.spec, SessionParams: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	id, previous := opened.SessionID, "initial"
	for i, units := range []uint64{90, 150, 210, 270} {
		if _, err := h.engine.ProcessEvent(ctx, id, usageEvent(fmt.Sprintf("event-%d", i), uint64(i+1), units)); err != nil {
			t.Fatal(err)
		}
		next := fmt.Sprintf("revision-%d", i+1)
		wire := realRevisionWire(t, h, next, previous, uint64(180+60*i), false, uint64(i+1))
		first, err := h.engine.ReviseAuthorization(ctx, id, next, wire, nil, big.NewInt(1200))
		if err != nil {
			t.Fatal(err)
		}
		// The first HTTP/WS response could be lost. Retry the exact request
		// after receiver and broker restart without another debit/admission.
		restartRevisionHarness(t, h)
		restart()
		second, err := h.engine.ReviseAuthorization(ctx, id, next, wire, nil, big.NewInt(1200))
		if err != nil || !first.Lease.Equal(second.Lease) || first.Revision.ObservedAt != second.Revision.ObservedAt {
			t.Fatalf("replay: %v", err)
		}
		rec, _ := h.store.Get(id)
		if rec.AccountAuthorizationID != next || rec.AuthorizationMaxUnits != uint64(180+60*i) || rec.State != sessionstore.StateActive {
			t.Fatal("refill did not extend admitted authority")
		}
		previous = next
	}
	if _, err := h.engine.ProcessEvent(ctx, id, usageEvent("final-usage", 5, 310)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.End(ctx, id, ReasonGatewayClose); err != nil {
		t.Fatal(err)
	}
	rec, _ := h.store.Get(id)
	account, err := client.GetWholesaleAccount(ctx, rec.Sender)
	if err != nil || account.Debited.Cmp(big.NewInt(3100)) != 0 || account.Reserved.Sign() != 0 {
		t.Fatalf("account: %+v %v", account, err)
	}
	first, err := h.engine.RecordSettlement(ctx, id)
	if err != nil || first.GetSettlementSeq() == 0 || first.GetAuthorizationId() != previous || first.GetDebitedUnits() != 310 {
		t.Fatal("invalid terminal evidence", err)
	}
	h.advance(time.Hour)
	restartRevisionHarness(t, h)
	second, err := h.engine.RecordSettlement(ctx, id)
	if err != nil || !proto.Equal(first, second) {
		t.Fatal("terminal evidence changed after restart", err)
	}
}

func TestRealRefusedRevisionExhaustionFreezesTerminalEvidence(t *testing.T) {
	client, restart := receiverForRevision(t)
	h := newHarness(t)
	h.engine.cfg.Payment = client
	h.nowVal = time.Now().UTC()
	h.spec.PricePerWorkUnitWei = big.NewInt(10)
	h.spec.MinRunwayUnits = 120
	ctx := context.Background()
	opened, err := h.engine.Open(ctx, OpenRequest{RequestID: "initial", GatewaySessionID: "gws-1", AuthorizationBytes: realRevisionWire(t, h, "initial", "", 120, false), InitialReservationWei: big.NewInt(1200), Spec: h.spec, SessionParams: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	id := opened.SessionID
	// Thirteen advances, followed by one coarse tick across the cap, produce
	// receiver settlement sequence15. Broker publication has its own sequence.
	for i := 1; i <= 13; i++ {
		if _, err := h.engine.ProcessEvent(ctx, id, usageEvent(fmt.Sprint(i), uint64(i), uint64(i*8))); err != nil {
			t.Fatal(err)
		}
	}
	// A valid successor with deliberately malformed optional funding exercises
	// a real receiver refusal followed by canceled_unused. This synthetic cause
	// is not a claim about the unidentified production refusal.
	wire := realRevisionWire(t, h, "refused", "initial", 180, false)
	_, err = h.engine.ReviseAuthorization(ctx, id, "refused", wire, []byte("malformed-test-funding"), big.NewInt(1200))
	var refused *ProtocolError
	if !errors.As(err, &refused) || refused.Revision == nil || refused.Revision.Reason != "FUNDING_MALFORMED" {
		t.Fatalf("refused: %v", err)
	}
	restartRevisionHarness(t, h)
	restart()
	before, _ := h.store.Get(id)
	fenced, err := client.GetSpendAuthorization(ctx, before.Sender, "refused")
	if err != nil || fenced.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_CANCELED_UNUSED) {
		t.Fatal("successor fence did not survive restart", err)
	}
	if _, err := h.engine.ProcessEvent(ctx, id, usageEvent("exhaust", 14, 124)); err != nil {
		t.Fatal(err)
	}
	rec, _ := h.store.Get(id)
	if !rec.Terminal() || !rec.PaymentClosed || !rec.RunnerTerminated || rec.AccountAuthorizationID != "initial" {
		t.Fatal("incorrect terminal authority")
	}
	state, err := client.GetSpendAuthorization(ctx, rec.Sender, "initial")
	if err != nil || state.SettlementSeq != 15 {
		t.Fatal("receiver sequence mismatch", err)
	}
	first, err := h.engine.RecordSettlement(ctx, id)
	if err != nil || first.GetSettlementSeq() != 1 || first.GetClaimedUnits() != 124 || first.GetDebitedUnits() != 120 || first.GetActualUnits() != 120 || first.GetBreakdown()["claim_debit_gap_reason"] != "authorization_cap" {
		t.Fatalf("terminal: %+v %v", first, err)
	}
	for i := 0; i < 3; i++ {
		h.advance(time.Minute)
		restartRevisionHarness(t, h)
		restart()
		if _, err := h.engine.End(ctx, id, "different_reason"); err != nil {
			t.Fatal(err)
		}
		second, err := h.engine.RecordSettlement(ctx, id)
		if err != nil || !proto.Equal(first, second) {
			t.Fatal("terminal replay differs", err)
		}
	}
	// Upgrade repair for a pre-fix terminal record: never substitute receiver15.
	if err := h.store.Update(id, func(r *sessionstore.Record) error { r.TerminalSettlement = nil; r.SettlementSeq = 0; return nil }); err != nil {
		t.Fatal(err)
	}
	repaired, err := h.engine.RecordSettlement(ctx, id)
	if err != nil || repaired.GetSettlementSeq() != 1 || repaired.GetDebitedUnits() != 120 {
		t.Fatal("legacy terminal repair", err)
	}
}
