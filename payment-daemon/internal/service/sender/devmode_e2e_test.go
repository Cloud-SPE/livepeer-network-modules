package sender_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devbroker"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devclock"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devkeystore"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/receiver"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/sender"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
	daemonTypes "github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/types"
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

// The advertised minimum face value must actually credit something.
//
// It did not. minFaceValue was floor(MaxWinProb/win_prob); credit is
// floor(face_value x win_prob / MaxWinProb). MaxWinProb is odd, so at the
// default win_prob (MaxWinProb/1024) the floor came out at 1024 and
// 1024 x win_prob < MaxWinProb — the payee accepted its own advertised
// minimum and credited zero. A gateway funding at exactly the advertised
// figure bought work for nothing and then saw insufficient_balance,
// which is how LOC found it. The correct minimum is 1025.
//
// This asks the daemon what its minimum is rather than hardcoding one,
// so it keeps holding if the defaults move: whatever face value this
// payee advertises as sufficient, a ticket at that value must credit at
// least one wei.
func TestAdvertisedMinimumFaceValueCreditsAtLeastOneWei(t *testing.T) {
	recipient := bytes20(0xdf)
	// Deliberately the DEFAULT config, not receiverStand's: the bug is
	// in the default win probability's derived minimum, and a stand that
	// pins win_prob to MaxWinProb makes the minimum 1 and hides it.
	payee, cleanup := defaultConfigReceiverStand(t, recipient)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Ask for an impossible face value; the refusal names the minimum.
	_, err := payee.GetTicketParams(ctx, &pb.GetTicketParamsRequest{
		Sender:     bytes20(0x01),
		Recipient:  recipient,
		FaceValue:  big.NewInt(1).Bytes(),
		Capability: "openai:chat-completions",
		Offering:   "model-a",
	})
	if err == nil {
		t.Fatal("a 1 wei face value was accepted; expected a refusal naming the minimum")
	}
	minFace := parseAdvertisedMinimum(t, err.Error())

	// The advertised minimum must be accepted...
	params, err := payee.GetTicketParams(ctx, &pb.GetTicketParamsRequest{
		Sender:     bytes20(0x01),
		Recipient:  recipient,
		FaceValue:  minFace.Bytes(),
		Capability: "openai:chat-completions",
		Offering:   "model-a",
	})
	if err != nil {
		t.Fatalf("payee refused its own advertised minimum of %s wei: %v", minFace, err)
	}

	// ...and must credit at least one wei, which is the entire point of
	// having a minimum. This is the receiver's own arithmetic, not a
	// reimplementation: floor(face x win / MaxWinProb).
	winProb := new(big.Int).SetBytes(params.GetTicketParams().GetWinProb())
	credit := new(big.Int).Quo(new(big.Int).Mul(minFace, winProb), daemonTypes.MaxWinProb)
	if credit.Sign() == 0 {
		t.Fatalf("advertised minimum %s wei credits ZERO at win_prob %s — "+
			"a gateway funding exactly this buys work for free",
			minFace, winProb)
	}

	// And one wei below it must be refused, or the boundary is not a
	// boundary — an off-by-one in the safe direction is still wrong.
	below := new(big.Int).Sub(minFace, big.NewInt(1))
	if _, err := payee.GetTicketParams(ctx, &pb.GetTicketParamsRequest{
		Sender:     bytes20(0x01),
		Recipient:  recipient,
		FaceValue:  below.Bytes(),
		Capability: "openai:chat-completions",
		Offering:   "model-a",
	}); err == nil {
		t.Fatalf("payee accepted %s wei, one below its advertised minimum", below)
	}
}

// parseAdvertisedMinimum pulls the figure out of the payee's refusal:
// "requested face_value N wei is below this payee's minimum of M wei".
func parseAdvertisedMinimum(t *testing.T, msg string) *big.Int {
	t.Helper()
	const marker = "minimum of "
	i := strings.Index(msg, marker)
	if i < 0 {
		t.Fatalf("refusal did not name a minimum: %s", msg)
	}
	rest := msg[i+len(marker):]
	j := strings.Index(rest, " ")
	if j < 0 {
		t.Fatalf("could not parse the minimum out of: %s", msg)
	}
	got, ok := new(big.Int).SetString(rest[:j], 10)
	if !ok {
		t.Fatalf("minimum %q is not a decimal integer", rest[:j])
	}
	return got
}

// defaultConfigReceiverStand runs a receiver on its shipped defaults —
// no face value or win probability override — because those defaults are
// what the derived minimum is computed from.
func defaultConfigReceiverStand(t *testing.T, recipient []byte) (pb.PayeeDaemonClient, func()) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "rx.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	svc := receiver.New(st, receiver.Config{Recipient: recipient}, nil)

	sockPath := filepath.Join(dir, "rx.sock")
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
	return pb.NewPayeeDaemonClient(conn), func() {
		_ = conn.Close()
		gs.GracefulStop()
		_ = st.Close()
	}
}

// A funding intent must actually be funded.
//
// A ticket credits its EXPECTED value — floor(face x win_prob /
// MaxWinProb), about face/1024 at the default probability — and exactly
// one ticket was minted regardless of funded_value_wei. So a 3,000 wei
// intent bought 2 wei of credit: the caller funded 512x less than it
// believed, then hit insufficient_balance after the exchange had already
// been admitted.
//
// The face value cannot be raised to fix it: the payee fixes face value
// when the session opens, and a resize must not move work_id
// (TestRefillSizingDoesNotChangeSessionIdentity). So the batch grows in
// tickets instead — same session, same face value, N times the credit.
//
// Checked end to end rather than on the mint alone: the contract is that
// the PAYEE credits at least what was funded, and only the payee can say
// what it credited.
func TestFundingIntentIsActuallyFunded(t *testing.T) {
	for _, funded := range []int64{
		1025,      // the advertised minimum
		3000,      // the reported case
		4097,      // non-divisible by the per-ticket credit
		1_000_000, // comfortably many tickets
	} {
		t.Run(fmt.Sprintf("funded_%d", funded), func(t *testing.T) {
			recipient := bytes20(0xf0)
			// The DEFAULT win probability — the whole defect lives in
			// the 1/1024 rounding, and a stand pinning MaxWinProb makes
			// one ticket sufficient and hides it.
			payee, cleanupPayee := defaultConfigReceiverStand(t, recipient)
			defer cleanupPayee()
			payer, baseURL, cleanupPayer := devModeSenderStand(t, payee)
			defer cleanupPayer()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			req := devModeCreateRequest(recipient, fmt.Sprintf("fund-%d", funded), baseURL)
			req.Funding.FundedValueWei = &pb.BigUInt{Value: big.NewInt(funded).Bytes()}
			created, err := payer.CreatePayment(ctx, req)
			if err != nil {
				t.Fatalf("CreatePayment for %d wei: %v", funded, err)
			}

			// funded_value_wei echoes the request exactly.
			if got := new(big.Int).SetBytes(created.GetFundedValueWei().GetValue()); got.Int64() != funded {
				t.Fatalf("funded_value_wei echoed %s; want %d", got, funded)
			}
			// expected_value covers it.
			ev := new(big.Int).SetBytes(created.GetExpectedValue().GetValue())
			if ev.Cmp(big.NewInt(funded)) < 0 {
				t.Fatalf("expected_value %s < funded_value_wei %d (%d tickets) — "+
					"the caller funded less than it asked for",
					ev, funded, created.GetTicketsCreated())
			}

			// And the PAYEE independently credits at least that much.
			var pay pb.Payment
			if err := proto.Unmarshal(created.GetPaymentBytes(), &pay); err != nil {
				t.Fatal(err)
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
				PaymentBytes: created.GetPaymentBytes(),
				WorkId:       workID,
			})
			if err != nil {
				t.Fatalf("ProcessPayment: %v", err)
			}
			if resp.GetTicketsRejected() != 0 {
				t.Fatalf("payee rejected %d of %d tickets (%s)",
					resp.GetTicketsRejected(), len(resp.GetTicketStatus()),
					resp.GetDominantRejection())
			}
			credited := new(big.Int).SetBytes(resp.GetCreditedEv())
			if credited.Cmp(big.NewInt(funded)) < 0 {
				t.Fatalf("payee credited %s for a %d wei intent across %d tickets — "+
					"the sender's promise and the ledger disagree",
					credited, funded, created.GetTicketsCreated())
			}
		})
	}
}
