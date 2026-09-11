package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type recoveryPayer struct {
	pb.PayerDaemonClient
	response *pb.CreatePaymentResponse
	wantID   string
	wantTgt  string
	wantObs  string
}

func (p *recoveryPayer) CreatePayment(_ context.Context, req *pb.CreatePaymentRequest, _ ...grpc.CallOption) (*pb.CreatePaymentResponse, error) {
	if req.GetMintRequestId() != p.wantID || new(big.Int).SetBytes(req.GetAccountFunding().GetTargetAvailableWei().GetValue()).String() != p.wantTgt ||
		new(big.Int).SetBytes(req.GetAccountFunding().GetObservedAvailableWei().GetValue()).String() != p.wantObs {
		return nil, fmt.Errorf("mint replay request differs")
	}
	return proto.Clone(p.response).(*pb.CreatePaymentResponse), nil
}

type recoveryPayee struct {
	pb.PayeeDaemonClient
	payer       []byte
	authID      string
	maxDebit    *big.Int
	settleCalls int
}

func (p *recoveryPayee) GetSpendAuthorization(_ context.Context, req *pb.GetSpendAuthorizationRequest, _ ...grpc.CallOption) (*pb.GetSpendAuthorizationResponse, error) {
	if hex.EncodeToString(req.GetPayer()) != hex.EncodeToString(p.payer) || req.GetAuthorizationId() != p.authID {
		return nil, fmt.Errorf("authorization lookup differs")
	}
	if p.settleCalls > 0 {
		return &pb.GetSpendAuthorizationResponse{
			State: pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED, SettlementSeq: 1,
		}, nil
	}
	return &pb.GetSpendAuthorizationResponse{State: pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED,
		ReservedValueWei: &pb.BigUInt{Value: p.maxDebit.Bytes()}}, nil
}

func (p *recoveryPayee) SettleAuthorization(_ context.Context, req *pb.SettleAuthorizationRequest, _ ...grpc.CallOption) (*pb.SettleAuthorizationResponse, error) {
	if req.GetAuthorizationId() != p.authID || req.GetActualUnits() != 0 || req.GetSettlementSeq() != 1 {
		return nil, fmt.Errorf("settlement request differs")
	}
	p.settleCalls++
	return &pb.SettleAuthorizationResponse{Replayed: p.settleCalls > 1}, nil
}

func TestVerifyWholesaleRecoveryAcrossRestart(t *testing.T) {
	payerAddress := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	paymentBytes, err := proto.Marshal(&pb.Payment{Sender: payerAddress})
	if err != nil {
		t.Fatal(err)
	}
	paymentHash := sha256.Sum256(paymentBytes)
	maxDebit := big.NewInt(60)
	initial := wholesaleAccount{
		Payer: "0x" + hex.EncodeToString(payerAddress), Payee: "0x0000000000000000000000000000000000000002",
		ChainID: 42161, Denomination: "wei", Credited: "100", Reserved: "60", Debited: "10", Available: "30", Version: 7,
	}
	final := initial
	final.Reserved, final.Available, final.Version = "0", "90", 8

	var mu sync.Mutex
	accountQueries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/payment/account":
			mu.Lock()
			accountQueries++
			current := initial
			if accountQueries > 1 {
				current = final
			}
			mu.Unlock()
			_ = jsonResponse(w, current)
		case "/v1/settlement/job-recovery":
			billed := base64.StdEncoding.EncodeToString(big.NewInt(10).Bytes())
			envelope := fmt.Sprintf(`{"payload":{"job_id":"job-recovery","request_id":"request-completed","outcome":"TOPPED_UP","billed_value_wei":{"value":%q}}}`, billed)
			w.Header().Set("Livepeer-Settlement", base64.StdEncoding.EncodeToString([]byte(envelope)))
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cp := &wholesaleRecoveryCheckpoint{
		Version: wholesaleRecoveryCheckpointVersion, Phase: "admitted",
		Payer: initial.Payer, AuthorizationID: "held-auth", MintRequestID: "held-mint",
		TargetAvailableWei: "90", ObservedAvailableWei: "30", MaxDebitWei: maxDebit.String(),
		PaymentSHA256: hex.EncodeToString(paymentHash[:]), ExpectedValueWei: "60", TicketsCreated: 1,
		AccountAfterAdmission: initial, SettlementJobID: "job-recovery", SettlementRequestID: "request-completed",
		SettlementState: "TOPPED_UP", SettlementBilledWei: "10",
	}
	checkpoint := filepath.Join(t.TempDir(), "recovery-test.json")
	if err := writeRecoveryCheckpoint(checkpoint, cp, true); err != nil {
		t.Fatal(err)
	}
	payer := &recoveryPayer{
		response: &pb.CreatePaymentResponse{PaymentBytes: paymentBytes, TicketsCreated: 1, ExpectedValue: &pb.BigUInt{Value: maxDebit.Bytes()}},
		wantID:   "held-mint", wantTgt: "90", wantObs: "30",
	}
	payee := &recoveryPayee{payer: payerAddress, authID: "held-auth", maxDebit: maxDebit}
	cfg := config{brokerURL: server.URL, recipient: []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}, checkpointFile: checkpoint}
	if err := verifyWholesaleRecovery(context.Background(), cfg, payer, payee); err != nil {
		t.Fatal(err)
	}
	verified, err := readRecoveryCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Phase != "verified" || verified.VerifiedAt == "" || payee.settleCalls != 2 {
		t.Fatalf("checkpoint=%+v settle_calls=%d", verified, payee.settleCalls)
	}
	if err := probeWholesaleEvidence(context.Background(), config{brokerURL: server.URL, checkpointDir: filepath.Dir(checkpoint)}, payee); err != nil {
		t.Fatal(err)
	}
}

func jsonResponse(w http.ResponseWriter, value any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(value)
}
