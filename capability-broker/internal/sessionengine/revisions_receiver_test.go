package sessionengine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/settlement"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const revisionTestKey = "0101010101010101010101010101010101010101010101010101010101010101"

func realRevisionWire(t *testing.T, h *harness, id, predecessor string, units uint64, expired bool) []byte {
	t.Helper()
	key, _ := crypto.HexToECDSA(revisionTestKey)
	now := time.Now()
	end := now.Add(time.Hour)
	if expired {
		end = now.Add(-time.Second)
	}
	price := h.spec.PricePerWorkUnitWei
	per := h.spec.PerUnits
	if per == 0 {
		per = 1
	}
	p := &pb.SpendAuthorizationPayload{Domain: "livepeer-spend-authorization/v2", SettlementDomainId: payment.MockSettlementDomainID,
		Payer: crypto.PubkeyToAddress(key.PublicKey).Bytes(), Payee: bytes.Repeat([]byte{0xaa}, 20), AuthorizationId: id, RequestId: id, SessionId: "gws-1", BrokerUri: "https://broker.example.com", Protocol: "paid-session/v1", Capability: h.spec.Capability, Offering: h.spec.Offering, ChainId: 42161, Denomination: "wei",
		AcceptedPrice: &pb.AcceptedPrice{Capability: h.spec.Capability, Offering: h.spec.Offering, PricePerUnitWei: &pb.BigUInt{Value: price.Bytes()}, UnitsPerPrice: per, WorkUnitName: h.spec.WorkUnit}, MaxTotalUnits: units, MaxDebitWei: &pb.BigUInt{Value: payment.BillFor(price, per, units).Bytes()}, RequestDigest: make([]byte, 32), NotBefore: now.Add(-time.Hour).Format(time.RFC3339Nano), ExpiresAt: end.Format(time.RFC3339Nano)}
	if predecessor != "" {
		p.PredecessorAuthorizationId, p.Revision = predecessor, 1
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := crypto.Sign(crypto.Keccak256([]byte("\x19Ethereum Signed Message:\n32"), crypto.Keccak256(raw)), key)
	if err != nil {
		t.Fatal(err)
	}
	sig[64] += 27
	wire, err := proto.Marshal(&pb.SpendAuthorization{Payload: p, Signature: sig})
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func receiverForRevision(t *testing.T) (*payment.GRPC, func()) {
	t.Helper()
	binary := os.Getenv("SESSION_RECEIVER_FIXTURE_BIN")
	if binary == "" {
		t.Skip("run make test-revisions for real receiver integration")
	}
	dir, err := os.MkdirTemp("", "revision-rx-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	key, _ := crypto.HexToECDSA(revisionTestKey)
	var cmd *exec.Cmd
	start := func() {
		cmd = exec.Command(binary, "-test.run=^TestSessionReceiverFixture$", "-test.timeout=10m")
		cmd.Env = append(os.Environ(), "SESSION_RECEIVER_FIXTURE_DIR="+dir, "SESSION_RECEIVER_FIXTURE_PAYER="+hex.EncodeToString(crypto.PubkeyToAddress(key.PublicKey).Bytes()))
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(dir, "receiver.sock")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("receiver fixture did not start")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	stop := func() {
		if cmd != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			if err := cmd.Wait(); err != nil {
				t.Errorf("fixture exit: %v", err)
			}
			cmd = nil
		}
	}
	start()
	t.Cleanup(stop)
	client, err := payment.NewGRPC(context.Background(), filepath.Join(dir, "receiver.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Shutdown() })
	return client, func() { stop(); _ = os.Remove(filepath.Join(dir, "receiver.sock")); start() }
}

type lossyReceiver struct {
	*payment.GRPC
	loseAdmission, loseSettlement bool
}

func (r *lossyReceiver) AdmitAuthorization(ctx context.Context, req payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	result, err := r.GRPC.AdmitAuthorization(ctx, req)
	if err == nil && r.loseAdmission {
		r.loseAdmission = false
		return nil, status.Error(codes.Unavailable, "injected lost admission response")
	}
	return result, err
}
func (r *lossyReceiver) SettleAuthorization(ctx context.Context, req payment.SettleAuthorizationRequest) (*payment.SettleAuthorizationResult, error) {
	result, err := r.GRPC.SettleAuthorization(ctx, req)
	if err == nil && r.loseSettlement {
		r.loseSettlement = false
		return nil, status.Error(codes.Unavailable, "injected lost settlement response")
	}
	return result, err
}

func TestRevisionRealReceiver(t *testing.T) {
	for _, scenario := range []string{"new_concurrent", "old_intent", "lost_admission", "winding_down_unadmitted", "winding_down_admitted", "expired", "lost_settlement", "receiver_ahead"} {
		t.Run(scenario, func(t *testing.T) {
			client, restartReceiver := receiverForRevision(t)
			h := newHarness(t)
			h.spec.PricePerWorkUnitWei = big.NewInt(1_000_000_000_000)
			h.spec.MinRunwayUnits = 120
			h.nowVal = time.Now().UTC()
			remote := &lossyReceiver{GRPC: client}
			h.engine.cfg.Payment = remote
			ctx := context.Background()
			opened, err := h.engine.Open(ctx, OpenRequest{RequestID: "original", GatewaySessionID: "gws-1", AuthorizationBytes: realRevisionWire(t, h, "original", "", 120, false), InitialReservationWei: big.NewInt(120_000_000_000_000), Spec: h.spec, SessionParams: json.RawMessage(`{}`), CapacityRef: "cap-slot"})
			if err != nil {
				t.Fatal(err)
			}
			id := opened.SessionID
			if _, err = h.engine.ProcessEvent(ctx, id, usageEvent("usage-74", 1, 74)); err != nil {
				t.Fatal(err)
			}
			wire := realRevisionWire(t, h, "successor", "original", 180, scenario == "expired")
			requested := big.NewInt(120_000_000_000_000)
			wantAuth := "successor"
			switch scenario {
			case "old_intent", "winding_down_unadmitted", "expired":
				if scenario != "expired" {
					_, err := client.AdmitAuthorization(ctx, payment.AdmitAuthorizationRequest{AuthorizationBytes: wire, Reservation: requested})
					if status.Code(err) != codes.FailedPrecondition || payment.AdmissionFailureReason(err) != "RESERVATION_EXCEEDS_REMAINING_DEBIT" {
						t.Fatalf("reservation contract: %v", err)
					}
				}
				fp := sha256.New()
				fp.Write(wire)
				fp.Write([]byte{0})
				if err = h.store.Update(id, func(r *sessionstore.Record) error {
					r.RevisionIntent = &sessionstore.RevisionIntent{RequestID: "revision", AuthorizationBytes: wire, Fingerprint: fp.Sum(nil), ReservationWei: requested.String(), LeaseExpiresAt: h.now().Add(30 * time.Second)}
					if scenario == "winding_down_unadmitted" {
						r.State, r.CloseReason = sessionstore.StateWindingDown, ReasonHeartbeatLost
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				restartRevisionHarness(t, h)
				restartReceiver()
				h.engine.Recover(ctx)
				if scenario != "old_intent" {
					wantAuth = "original"
				}
			case "lost_admission", "winding_down_admitted":
				remote.loseAdmission = true
				if _, err = h.engine.ReviseAuthorization(ctx, id, "revision", wire, nil, requested); err == nil {
					t.Fatal("expected lost response")
				}
				restartRevisionHarness(t, h)
				restartReceiver()
				if scenario == "winding_down_admitted" {
					_, _ = h.engine.End(ctx, id, ReasonHeartbeatLost)
				}
				h.advance(2 * time.Second)
				h.engine.Recover(ctx)
			default:
				if scenario == "receiver_ahead" {
					// Simulate a durable debit whose broker commit was lost. Keep the
					// receiver at 74 and roll back only the broker's recorded high watermark.
					if err = h.store.Update(id, func(r *sessionstore.Record) error {
						r.DebitedTotal, r.ClaimedTotal, r.DebitSeq, r.PendingDebitSeq = 0, 0, 0, 1
						r.BilledWei = "0"
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				var wg sync.WaitGroup
				errors := make(chan error, 8)
				for i := 0; i < 8; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_, err := h.engine.ReviseAuthorization(ctx, id, "revision", wire, nil, requested)
						errors <- err
					}()
				}
				wg.Wait()
				close(errors)
				for err := range errors {
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			rec, err := h.store.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			if rec.RevisionIntent != nil || rec.AccountAuthorizationID != wantAuth || rec.BilledWei != "74000000000000" || rec.DebitedTotal != 74 {
				t.Fatalf("recovery intent=%v authority=%s units=%d billed=%s", rec.RevisionIntent != nil, rec.AccountAuthorizationID, rec.DebitedTotal, rec.BilledWei)
			}
			if !rec.Closing() && wantAuth == "successor" && rec.AuthorizationReservedWei != "106000000000000" {
				t.Fatalf("reservation=%s", rec.AuthorizationReservedWei)
			}
			if scenario == "expired" || scenario == "winding_down_unadmitted" {
				if _, err := h.engine.ReviseAuthorization(ctx, id, "revision", wire, nil, requested); err == nil {
					t.Fatal("refused revision replay accepted")
				}
			}
			if scenario == "lost_settlement" {
				remote.loseSettlement = true
			}
			if _, err = h.engine.End(ctx, id, ReasonHeartbeatLost); err != nil {
				t.Fatal(err)
			}
			restartRevisionHarness(t, h)
			h.engine.Recover(ctx)
			rec, err = h.store.Get(id)
			if err != nil || !rec.Terminal() || !rec.PaymentClosed || rec.CloseReason != ReasonHeartbeatLost {
				t.Fatalf("winddown state=%s closed=%v reason=%s err=%v", rec.State, rec.PaymentClosed, rec.CloseReason, err)
			}
			account, err := client.GetWholesaleAccount(ctx, rec.Sender)
			if err != nil || account.Debited.String() != "74000000000000" || account.Reserved.Sign() != 0 {
				t.Fatalf("final account=%+v err=%v", account, err)
			}
			record, err := h.engine.RecordSettlement(ctx, id)
			if err != nil || record.GetState() != "closed" || record.GetDebitedUnits() != 74 || new(big.Int).SetBytes(record.GetBilledValueWei().GetValue()).String() != "74000000000000" {
				t.Fatal("incorrect final settlement", err)
			}
			keyPath := filepath.Join(t.TempDir(), "signer.key")
			if err = os.WriteFile(keyPath, []byte(revisionTestKey), 0600); err != nil {
				t.Fatal(err)
			}
			signer, err := settlement.LoadSigner(keyPath)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := settlement.Encode(record, signer)
			if err != nil || encoded == "" {
				t.Fatal("signed settlement missing", err)
			}
			envelopeBytes, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatal(err)
			}
			var envelope settlement.Envelope
			if err := json.Unmarshal(envelopeBytes, &envelope); err != nil || envelope.Signature == nil {
				t.Fatal("settlement signature missing", err)
			}
			sig, err := hex.DecodeString(strings.TrimPrefix(envelope.Signature.Value, "0x"))
			if err != nil || len(sig) != 65 {
				t.Fatal("bad settlement signature", err)
			}
			if sig[64] >= 27 {
				sig[64] -= 27
			}
			digest := crypto.Keccak256([]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(envelope.Payload))), envelope.Payload)
			pub, err := crypto.SigToPub(digest, sig)
			if err != nil || "0x"+hex.EncodeToString(crypto.FromECDSAPub(pub)) != signer.PublicKeyHex() {
				t.Fatal("settlement signature did not verify", err)
			}

		})
	}
}

func TestRevisionRealReceiverFractionalRunway(t *testing.T) {
	for _, c := range []struct {
		name                string
		original, successor uint64
		initial             int64
		reserved            string
	}{
		{"fractional", 6, 9, 2, "2"},
		{"remaining_units_already_paid", 3, 3, 1, "0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			client, _ := receiverForRevision(t)
			h := newHarness(t)
			h.nowVal = time.Now().UTC()
			h.spec.PricePerWorkUnitWei = big.NewInt(1)
			h.spec.PerUnits = 3
			h.spec.MinRunwayUnits = 120
			h.engine.cfg.Payment = client
			ctx := context.Background()
			opened, err := h.engine.Open(ctx, OpenRequest{RequestID: "original", GatewaySessionID: "gws-1", AuthorizationBytes: realRevisionWire(t, h, "original", "", c.original, false), InitialReservationWei: big.NewInt(c.initial), Spec: h.spec, SessionParams: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = h.engine.ProcessEvent(ctx, opened.SessionID, usageEvent("one-unit", 1, 1)); err != nil {
				t.Fatal("independent runway rounding exceeded remaining debit", err)
			}
			if _, err = h.engine.ReviseAuthorization(ctx, opened.SessionID, "revision", realRevisionWire(t, h, "successor", "original", c.successor, false), nil, big.NewInt(40)); err != nil {
				t.Fatal(err)
			}
			rec, err := h.store.Get(opened.SessionID)
			if err != nil || rec.BilledWei != "1" || rec.AuthorizationReservedWei != c.reserved {
				t.Fatalf("fractional state billed=%s reserved=%s err=%v", rec.BilledWei, rec.AuthorizationReservedWei, err)
			}
			if _, err = h.engine.End(ctx, opened.SessionID, ReasonGatewayClose); err != nil {
				t.Fatal(err)
			}
			rec, err = h.store.Get(opened.SessionID)
			if err != nil || !rec.Terminal() || rec.BilledWei != "1" {
				t.Fatal("fractional settlement drift", err)
			}
		})
	}
}
