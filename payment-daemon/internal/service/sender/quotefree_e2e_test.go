package sender_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devbroker"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devclock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/keystore/inmemory"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/receiver"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/sender"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/types"
)

func TestQuoteFreeSenderFetchesPayeeParamsAndReceiverAcceptsPayment(t *testing.T) {
	recipient := bytes20(0xab)
	payee, st, cleanupPayee := receiverStand(t, recipient)
	defer cleanupPayee()

	// Broker-style HTTP proxy for /v1/payment/ticket-params.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			SenderETHAddress    string `json:"sender_eth_address"`
			RecipientETHAddress string `json:"recipient_eth_address"`
			FaceValueWei        string `json:"face_value_wei"`
			Capability          string `json:"capability"`
			Offering            string `json:"offering"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		senderAddr := mustDecodeHexAddress(t, req.SenderETHAddress)
		recipientAddr := mustDecodeHexAddress(t, req.RecipientETHAddress)
		faceValue, _ := new(big.Int).SetString(req.FaceValueWei, 10)
		resp, err := payee.GetTicketParams(r.Context(), &pb.GetTicketParamsRequest{
			Sender:     senderAddr,
			Recipient:  recipientAddr,
			FaceValue:  faceValue.Bytes(),
			Capability: req.Capability,
			Offering:   req.Offering,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		out := map[string]any{
			"ticket_params": map[string]any{
				"recipient":           req.RecipientETHAddress,
				"face_value":          new(big.Int).SetBytes(resp.GetTicketParams().GetFaceValue()).String(),
				"win_prob":            new(big.Int).SetBytes(resp.GetTicketParams().GetWinProb()).String(),
				"recipient_rand_hash": "0x" + hex.EncodeToString(resp.GetTicketParams().GetRecipientRandHash()),
				"seed":                "0x" + hex.EncodeToString(resp.GetTicketParams().GetSeed()),
				"expiration_block":    "0",
				"expiration_params": map[string]any{
					"creation_round":            0,
					"creation_round_block_hash": "0x0000000000000000000000000000000000000000000000000000000000000000",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer proxy.Close()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "tx.sock")
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	keystore, err := inmemory.New(priv)
	if err != nil {
		t.Fatal(err)
	}
	svc := sender.New(keystore, devbroker.New(), devclock.New(), nil, sender.NewHTTPTicketParamsFetcher())

	lis, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer()
	pb.RegisterPayerDaemonServer(gs, svc)
	go func() { _ = gs.Serve(lis) }()
	defer gs.GracefulStop()

	conn, err := grpc.NewClient("unix://"+sockPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	payer := pb.NewPayerDaemonClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	createResp, err := payer.CreatePayment(ctx, &pb.CreatePaymentRequest{
		FaceValue:           big.NewInt(1000).Bytes(),
		Recipient:           recipient,
		Capability:          "openai:chat-completions",
		Offering:            "model-a",
		TicketParamsBaseUrl: proxy.URL,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	var pay pb.Payment
	if err := proto.Unmarshal(createResp.GetPaymentBytes(), &pay); err != nil {
		t.Fatalf("decode payment: %v", err)
	}
	workID := hex.EncodeToString(pay.GetTicketParams().GetRecipientRandHash())

	openResp, err := payee.OpenSession(ctx, &pb.OpenSessionRequest{
		WorkId:              workID,
		Capability:          "openai:chat-completions",
		Offering:            "model-a",
		PricePerWorkUnitWei: []byte{0x01},
		WorkUnit:            "tokens",
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if openResp.GetOutcome() != pb.OpenSessionResponse_OUTCOME_ALREADY_OPEN {
		t.Fatalf("OpenSession outcome = %s; want ALREADY_OPEN", openResp.GetOutcome())
	}

	processResp, err := payee.ProcessPayment(ctx, &pb.ProcessPaymentRequest{
		PaymentBytes: createResp.GetPaymentBytes(),
		WorkId:       workID,
	})
	if err != nil {
		t.Fatalf("ProcessPayment: %v", err)
	}
	if processResp.GetWinnersQueued() != 1 {
		t.Fatalf("WinnersQueued = %d; want 1", processResp.GetWinnersQueued())
	}
	if got := new(big.Int).SetBytes(processResp.GetCreditedEv()); got.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("CreditedEv = %s; want 1000", got)
	}

	pend, err := st.PendingRedemptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(pend) != 1 {
		t.Fatalf("pending redemptions = %d; want 1", len(pend))
	}
}

func receiverStand(t *testing.T, recipient []byte) (pb.PayeeDaemonClient, *store.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rx.db")
	sockPath := filepath.Join(dir, "rx.sock")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	svc := receiver.New(st, receiver.Config{
		Recipient:        recipient,
		DefaultFaceValue: big.NewInt(1000),
		DefaultWinProb:   types.MaxWinProb,
	}, nil)

	lis, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer()
	pb.RegisterPayeeDaemonServer(gs, svc)
	go func() { _ = gs.Serve(lis) }()

	conn, err := grpc.NewClient("unix://"+sockPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	cleanup := func() {
		_ = conn.Close()
		gs.GracefulStop()
		_ = st.Close()
	}
	return pb.NewPayeeDaemonClient(conn), st, cleanup
}

func bytes20(fill byte) []byte {
	out := make([]byte, 20)
	for i := range out {
		out[i] = fill
	}
	return out
}

func mustDecodeHexAddress(t *testing.T, raw string) []byte {
	t.Helper()
	b, err := hex.DecodeString(raw[2:])
	if err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return b
}
