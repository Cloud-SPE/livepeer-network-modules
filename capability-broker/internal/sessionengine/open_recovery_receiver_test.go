package sessionengine

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

type initialLossyReceiver struct {
	*payment.GRPC
	accepted, recoveryDown, loseSettle bool
	loseFence                          bool
	afterAdmission                     func()
}

func (p *initialLossyReceiver) AdmitAuthorization(ctx context.Context, r payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	if p.accepted {
		if _, err := p.GRPC.AdmitAuthorization(ctx, r); err != nil {
			return nil, err
		}
	}
	if p.afterAdmission != nil {
		p.afterAdmission()
	}
	return nil, errors.New("lost initial admission")
}
func (p *initialLossyReceiver) CancelAuthorizationAdmission(ctx context.Context, wire []byte) (*payment.CanceledAdmission, error) {
	if p.recoveryDown {
		return nil, errors.New("receiver unavailable")
	}
	result, err := p.GRPC.CancelAuthorizationAdmission(ctx, wire)
	if err == nil && p.loseFence {
		p.loseFence = false
		return nil, errors.New("lost fence response")
	}
	return result, err
}
func (p *initialLossyReceiver) SettleAuthorization(ctx context.Context, r payment.SettleAuthorizationRequest) (*payment.SettleAuthorizationResult, error) {
	result, err := p.GRPC.SettleAuthorization(ctx, r)
	if err == nil && p.loseSettle {
		p.loseSettle = false
		return nil, errors.New("lost settlement response")
	}
	return result, err
}
func TestInitialOpenRealReceiverRecovery(t *testing.T) {
	for _, scenario := range []string{"unaccepted", "accepted", "accepted_usage", "lost_settlement", "local_write_failed"} {
		t.Run(scenario, func(t *testing.T) {
			client, restartReceiver := receiverForRevision(t)
			h := newHarness(t)
			h.nowVal = time.Now().UTC()
			remote := &initialLossyReceiver{GRPC: client, accepted: scenario != "unaccepted", recoveryDown: true, loseSettle: scenario == "lost_settlement", loseFence: scenario == "unaccepted"}
			h.engine.cfg.Payment = remote
			req := OpenRequest{RequestID: "initial", GatewaySessionID: "gws-1", AuthorizationBytes: realRevisionWire(t, h, "initial", "", 100, false), InitialReservationWei: big.NewInt(500), Spec: h.spec, SessionParams: []byte(`{}`), CapacityRef: "slot"}
			otherWire := realRevisionWireForAccount(t, h, "other-account", "initial", "", 100, false)
			if _, err := client.AdmitAuthorization(context.Background(), payment.AdmitAuthorizationRequest{AuthorizationBytes: otherWire, Reservation: big.NewInt(500)}); err != nil {
				t.Fatal(err)
			}
			if scenario == "local_write_failed" {
				remote.afterAdmission = func() { _ = h.store.Close() }
			}
			if _, err := h.engine.Open(context.Background(), req); err == nil {
				t.Fatal("missing injected error")
			}
			var auth pb.SpendAuthorization
			_ = proto.Unmarshal(req.AuthorizationBytes, &auth)
			want := uint64(0)
			if scenario == "accepted_usage" {
				want = 7
				if _, err := client.AdvanceAuthorization(context.Background(), payment.AdvanceAuthorizationRequest{WholesaleAccountID: "test-account", Payer: auth.Payload.Payer, AuthorizationID: "initial", CumulativeUnits: want, AdvanceSeq: 4, TargetReserved: big.NewInt(400)}); err != nil {
					t.Fatal(err)
				}
			}
			restartRevisionHarness(t, h)
			restartReceiver()
			remote.recoveryDown = false
			h.engine.Recover(context.Background())
			h.engine.Sweep(context.Background())
			if scenario == "unaccepted" {
				held, err := h.store.Reservation("initial")
				if err != nil || !held.AdmissionCanceled {
					t.Fatalf("missing fence: %+v %v", held, err)
				}
				restartReceiver()
				if _, err = client.AdmitAuthorization(context.Background(), payment.AdmitAuthorizationRequest{AuthorizationBytes: req.AuthorizationBytes, Reservation: req.InitialReservationWei}); err == nil {
					t.Fatal("receiver accepted fenced authority after restart")
				}
			} else {
				id, err := h.store.SessionIDForRequest("initial")
				if err != nil {
					t.Fatal(err)
				}
				rec, _ := h.store.Get(id)
				if !rec.Terminal() || !rec.PaymentClosed || rec.DebitedTotal != want || rec.ClaimedTotal != want || h.runner.created != 0 {
					t.Fatalf("bad recovery: %+v", rec)
				}
				first, err := h.engine.RecordSettlement(context.Background(), id)
				if err != nil || first.GetWholesaleAccountId() != "test-account" || first.GetSettlementSeq() == 0 || first.GetDebitedUnits() != want {
					t.Fatalf("invalid evidence: %v %v", first, err)
				}
				account, err := client.GetWholesaleAccount(context.Background(), auth.Payload.Payer, "test-account")
				if err != nil || account.Reserved.Sign() != 0 || account.Debited.Cmp(big.NewInt(int64(want)*10)) != 0 {
					t.Fatalf("account=%+v %v", account, err)
				}
				restartRevisionHarness(t, h)
				restartReceiver()
				h.engine.Recover(context.Background())
				second, err := h.engine.RecordSettlement(context.Background(), id)
				if err != nil || !proto.Equal(first, second) {
					t.Fatal("terminal evidence changed after restart", err)
				}
			}
			other, err := client.GetSpendAuthorization(context.Background(), auth.Payload.Payer, "initial", "other-account")
			if err != nil || other.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) || other.Reserved.Cmp(big.NewInt(500)) != 0 || other.Billed.Sign() != 0 {
				t.Fatalf("recovery crossed account boundary: %+v %v", other, err)
			}
			otherAccount, err := client.GetWholesaleAccount(context.Background(), auth.Payload.Payer, "other-account")
			if err != nil || otherAccount.Debited.Sign() != 0 || otherAccount.Reserved.Cmp(big.NewInt(500)) != 0 {
				t.Fatalf("other account changed: %+v %v", otherAccount, err)
			}
			if len(h.release) != 1 || h.runner.created != 0 {
				t.Fatalf("duplicate effects release=%v runner=%d", h.release, h.runner.created)
			}
		})
	}
}
