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

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devbroker"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devclock"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devkeystore"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/sender"
)

// A chain-free sender and receiver must be able to exchange a payment.
//
// They could not. devkeystore emitted a synthetic SHA-256 vector with a
// hardcoded V=27, on the premise — written in its own doc comment — that
// "receivers in dev mode skip signature recovery." No such bypass exists:
// the receiver's validator always performs EIP-191 secp256k1 recovery, so
// every dev-mode ticket failed signature validation. The hermetic LOC
// matrix found this; no test here did, because every other end-to-end
// test wires the inmemory keystore, which holds a real key.
//
// So this test exists to use the DEV keystore specifically. Wiring it the
// way `--dev` wires it is the whole point — a regression that reaches for
// the production keystore would pass against the broken code.
func TestDevModeSenderAndReceiverExchangeAPayment(t *testing.T) {
	recipient := bytes20(0xcd)
	payee, st, cleanupPayee := receiverStand(t, recipient)
	defer cleanupPayee()

	payer, baseURL, cleanupPayer := devModeSenderStand(t, payee)
	defer cleanupPayer()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	createResp, err := payer.CreatePayment(ctx, devModeCreateRequest(recipient, "dev-mint-1", baseURL))
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	var pay pb.Payment
	if err := proto.Unmarshal(createResp.GetPaymentBytes(), &pay); err != nil {
		t.Fatalf("decode payment: %v", err)
	}
	workID := hex.EncodeToString(pay.GetTicketParams().GetRecipientRandHash())

	if _, err := payee.OpenSession(ctx, &pb.OpenSessionRequest{
		WorkId:              workID,
		Capability:          "openai:chat-completions",
		Offering:            "model-a",
		PricePerWorkUnitWei: big.NewInt(1000).Bytes(),
		WorkUnit:            "tokens",
	}); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	resp, err := payee.ProcessPayment(ctx, &pb.ProcessPaymentRequest{
		PaymentBytes: createResp.GetPaymentBytes(),
		WorkId:       workID,
	})
	if err != nil {
		t.Fatalf("ProcessPayment: %v", err)
	}
	// The failure mode being pinned is a ticket the receiver refuses, so
	// assert on what it accepted rather than on the call returning.
	received := len(resp.GetTicketStatus())
	if received == 0 {
		t.Fatal("receiver saw no tickets")
	}
	if resp.GetTicketsRejected() != 0 {
		t.Fatalf("receiver rejected %d of %d tickets (%s) — a dev-mode signature "+
			"must satisfy the same EIP-191 recovery a production one does",
			resp.GetTicketsRejected(), received, resp.GetDominantRejection())
	}
	if got := new(big.Int).SetBytes(resp.GetCreditedEv()); got.Sign() == 0 {
		t.Fatal("credited zero: a payment that credits nothing funds no work")
	}
	pend, err := st.PendingRedemptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(pend) != 1 {
		t.Fatalf("pending redemptions = %d; want 1", len(pend))
	}
}

// The security boundary has to stay a boundary. Making dev signatures
// real is only the right fix if a forged one is still refused — otherwise
// the suite would be exercising a check that cannot fail.
func TestDevModeTamperedSignatureIsRejected(t *testing.T) {
	recipient := bytes20(0xce)
	payee, st, cleanupPayee := receiverStand(t, recipient)
	defer cleanupPayee()

	payer, baseURL, cleanupPayer := devModeSenderStand(t, payee)
	defer cleanupPayer()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	createResp, err := payer.CreatePayment(ctx, devModeCreateRequest(recipient, "dev-mint-tamper", baseURL))
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	var pay pb.Payment
	if err := proto.Unmarshal(createResp.GetPaymentBytes(), &pay); err != nil {
		t.Fatalf("decode payment: %v", err)
	}
	if len(pay.GetTicketSenderParams()) == 0 {
		t.Fatal("payment carries no ticket sender params")
	}
	// Flip a bit in R. The signature stays 65 bytes with a valid V, so
	// this is refused by recovery rather than by a shape check — which is
	// the check that matters.
	sig := pay.GetTicketSenderParams()[0].GetSig()
	if len(sig) != 65 {
		t.Fatalf("signature is %d bytes; want 65", len(sig))
	}
	sig[7] ^= 0xff

	workID := hex.EncodeToString(pay.GetTicketParams().GetRecipientRandHash())
	if _, err := payee.OpenSession(ctx, &pb.OpenSessionRequest{
		WorkId:              workID,
		Capability:          "openai:chat-completions",
		Offering:            "model-a",
		PricePerWorkUnitWei: big.NewInt(1000).Bytes(),
		WorkUnit:            "tokens",
	}); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	tampered, err := proto.Marshal(&pay)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := payee.ProcessPayment(ctx, &pb.ProcessPaymentRequest{
		PaymentBytes: tampered,
		WorkId:       workID,
	})
	if err != nil {
		// A hard error is an acceptable refusal too.
		return
	}
	received := int32(len(resp.GetTicketStatus()))
	if resp.GetTicketsRejected() != received {
		t.Fatalf("receiver accepted %d of %d tickets carrying a forged signature",
			received-resp.GetTicketsRejected(), received)
	}
	if got := new(big.Int).SetBytes(resp.GetCreditedEv()); got.Sign() != 0 {
		t.Fatalf("credited %s wei for a forged signature", got)
	}
	pend, err := st.PendingRedemptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(pend) != 0 {
		t.Fatalf("queued %d redemptions for a forged signature", len(pend))
	}
}

// devModeSenderStand wires a sender exactly as `--dev` does: the dev
// keystore, the dev broker, the dev clock. The ticket-params fetch goes
// through an HTTP proxy onto the real payee, the way a broker relays it.
func devModeSenderStand(t *testing.T, payee pb.PayeeDaemonClient) (pb.PayerDaemonClient, string, func()) {
	t.Helper()

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
		faceValue, _ := new(big.Int).SetString(req.FaceValueWei, 10)
		resp, err := payee.GetTicketParams(r.Context(), &pb.GetTicketParamsRequest{
			Sender:     mustDecodeHexAddress(t, req.SenderETHAddress),
			Recipient:  mustDecodeHexAddress(t, req.RecipientETHAddress),
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

	keystore, err := devkeystore.New("")
	if err != nil {
		t.Fatalf("devkeystore.New: %v", err)
	}
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "tx.sock")
	svc := sender.New(keystore, devbroker.New(), devclock.New(), nil,
		sender.NewHTTPTicketParamsFetcher(), nil, mintStore(t), sender.Limits{})

	lis, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer()
	pb.RegisterPayerDaemonServer(gs, svc)
	go func() { _ = gs.Serve(lis) }()

	conn, err := grpc.NewClient("unix://"+sockPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return pb.NewPayerDaemonClient(conn), proxy.URL, func() {
		_ = conn.Close()
		gs.GracefulStop()
		proxy.Close()
	}
}

func devModeCreateRequest(recipient []byte, mintID, baseURL string) *pb.CreatePaymentRequest {
	return &pb.CreatePaymentRequest{
		Recipient:           recipient,
		MintRequestId:       mintID,
		TicketParamsBaseUrl: baseURL,
		AcceptedPrice: &pb.AcceptedPrice{
			PricePerUnitWei: &pb.BigUInt{Value: big.NewInt(1000).Bytes()},
			UnitsPerPrice:   1,
			WorkUnitName:    "tokens",
			Capability:      "openai:chat-completions",
			Offering:        "model-a",
			QuoteRef: &pb.QuoteRef{
				QuoteId:               "quote-dev",
				QuoteVersion:          1,
				ConstraintFingerprint: []byte{0x01},
				RouteFingerprint:      []byte{0x02},
			},
		},
		Funding: &pb.FundingIntent{
			EstimatedUnits: 1,
			FundedValueWei: &pb.BigUInt{Value: big.NewInt(1000).Bytes()},
			MaxTotalUnits:  1,
		},
	}
}
