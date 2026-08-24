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
	"sync"
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
	return devModeSenderStandVia(t, func() pb.PayeeDaemonClient { return payee })
}

// devModeSenderStandVia resolves the payee per call, so a test can
// restart it underneath the sender — which is what a restart test has to
// do, rather than leaving the sender talking to a closed connection.
func devModeSenderStandVia(t *testing.T, payeeFor func() pb.PayeeDaemonClient) (pb.PayerDaemonClient, string, func()) {
	c, u, _, stop := devModeSenderStandWithStore(t, payeeFor)
	return c, u, stop
}

// devModeSenderStandWithStore also hands back the payer's durable store,
// so a test can simulate the state loss that puts the payer's nonce
// watermark out of step with the payee's ledger.
func devModeSenderStandWithStore(t *testing.T, payeeFor func() pb.PayeeDaemonClient) (pb.PayerDaemonClient, string, *store.Store, func()) {
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
		resp, err := payeeFor().GetTicketParams(r.Context(), &pb.GetTicketParamsRequest{
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
	payerStore := mintStore(t)
	svc := sender.New(keystore, devbroker.New(), devclock.New(), nil,
		sender.NewHTTPTicketParamsFetcher(), nil, payerStore, sender.Limits{})

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
	return pb.NewPayerDaemonClient(conn), proxy.URL, payerStore, func() {
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

// A cached session must fund a LATER, larger request.
//
// LOC's reproducer: issue and process 1,025 wei, then issue 616,025 wei
// on the same payer/payee/route. The rescale that sizes a session's face
// value to its funding intent ran only when the session was created, so
// the second request was served from the cached 1,025-wei face value.
// Sizing the batch in tickets to compensate needed 601 of them, the payee
// rejected the last one at its 600-nonce cap, and 613,975 of 616,025 wei
// was credited — an under-funding the caller was never told about.
//
// The session is now re-quoted at a larger face value instead, keeping
// the tuple's recipient rand so work_id does not move.
func TestCachedSessionFundsALargerLaterRequest(t *testing.T) {
	recipient := bytes20(0xf3)
	payee, cleanupPayee := defaultConfigReceiverStand(t, recipient)
	defer cleanupPayee()
	payer, baseURL, cleanupPayer := devModeSenderStand(t, payee)
	defer cleanupPayer()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	issueAndProcess := func(mintID string, funded int64) (workID string, credited *big.Int) {
		t.Helper()
		req := devModeCreateRequest(recipient, mintID, baseURL)
		req.Funding.FundedValueWei = &pb.BigUInt{Value: big.NewInt(funded).Bytes()}
		created, err := payer.CreatePayment(ctx, req)
		if err != nil {
			t.Fatalf("CreatePayment(%d): %v", funded, err)
		}
		if ev := new(big.Int).SetBytes(created.GetExpectedValue().GetValue()); ev.Cmp(big.NewInt(funded)) < 0 {
			t.Fatalf("payer promised %s for a %d wei intent", ev, funded)
		}
		var pay pb.Payment
		if err := proto.Unmarshal(created.GetPaymentBytes(), &pay); err != nil {
			t.Fatal(err)
		}
		workID = hex.EncodeToString(pay.GetTicketParams().GetRecipientRandHash())
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
			t.Fatalf("ProcessPayment(%d): %v", funded, err)
		}
		if resp.GetTicketsRejected() != 0 {
			t.Fatalf("payee rejected %d of %d tickets funding %d wei (%s) — the batch did not "+
				"fit the session's remaining nonce budget",
				resp.GetTicketsRejected(), len(resp.GetTicketStatus()), funded,
				resp.GetDominantRejection())
		}
		return workID, new(big.Int).SetBytes(resp.GetCreditedEv())
	}

	firstWorkID, firstCredit := issueAndProcess("loc-small", 1025)
	if firstCredit.Cmp(big.NewInt(1025)) < 0 {
		t.Fatalf("first request credited %s of 1025", firstCredit)
	}

	secondWorkID, secondCredit := issueAndProcess("loc-large", 616025)
	if secondCredit.Cmp(big.NewInt(616025)) < 0 {
		t.Fatalf("the larger request credited %s of 616025 — a cached session under-funded it",
			secondCredit)
	}
	// Same session throughout: the resize is not allowed to move the
	// payee's identity for this route.
	if secondWorkID != firstWorkID {
		t.Fatalf("work_id moved on a resize: %s -> %s", firstWorkID, secondWorkID)
	}
}

// The nonce budget is cumulative over a session's whole life, so a
// long-lived route eventually rolls over.
//
// store.MaxSenderNonces applies per recipient rand, not per payment. So
// after that many accepted one-ticket payments the NEXT one was still
// minted, still looked successful to the caller, and was rejected whole
// with NONCE_CAP_REACHED for zero credit. Bounding a single batch does
// not see a budget spent across many.
//
// Rotation is coordinated: the payee owns the rand and mints the
// successor; the payer asks before signing and reports the predecessor
// so a changed work_id never arrives silently.
func TestSessionRollsOverAtTheNonceBudget(t *testing.T) {
	recipient := bytes20(0xf5)
	payee, cleanupPayee := defaultConfigReceiverStand(t, recipient)
	defer cleanupPayee()
	payer, baseURL, cleanupPayer := devModeSenderStand(t, payee)
	defer cleanupPayer()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	opened := map[string]bool{}
	pay := func(i int) (workID, predecessor string) {
		t.Helper()
		req := devModeCreateRequest(recipient, fmt.Sprintf("roll-%d", i), baseURL)
		req.Funding.FundedValueWei = &pb.BigUInt{Value: big.NewInt(2000).Bytes()}
		created, err := payer.CreatePayment(ctx, req)
		if err != nil {
			t.Fatalf("payment %d: %v", i, err)
		}
		workID = created.GetWorkId()
		predecessor = created.GetPredecessorWorkId()

		if !opened[workID] {
			if _, err := payee.OpenSession(ctx, &pb.OpenSessionRequest{
				WorkId:              workID,
				Capability:          "openai:chat-completions",
				Offering:            "model-a",
				PricePerWorkUnitWei: big.NewInt(1000).Bytes(),
				WorkUnit:            "tokens",
			}); err != nil {
				t.Fatalf("OpenSession at payment %d: %v", i, err)
			}
			opened[workID] = true
		}
		resp, err := payee.ProcessPayment(ctx, &pb.ProcessPaymentRequest{
			PaymentBytes: created.GetPaymentBytes(),
			WorkId:       workID,
		})
		if err != nil {
			t.Fatalf("ProcessPayment %d: %v", i, err)
		}
		if resp.GetTicketsRejected() != 0 {
			t.Fatalf("payment %d rejected %d of %d tickets (%s) — a payment the payer signed "+
				"and the payee refused whole",
				i, resp.GetTicketsRejected(), len(resp.GetTicketStatus()),
				resp.GetDominantRejection())
		}
		if credited := new(big.Int).SetBytes(resp.GetCreditedEv()); credited.Sign() == 0 {
			t.Fatalf("payment %d credited nothing", i)
		}
		return workID, predecessor
	}

	firstWorkID, _ := pay(1)
	var rolloverAt int
	var successor, reportedPredecessor string
	for i := 2; i <= store.MaxSenderNonces+1; i++ {
		w, pred := pay(i)
		if pred != "" {
			if rolloverAt != 0 {
				t.Fatalf("rolled over twice: at %d and again at %d", rolloverAt, i)
			}
			rolloverAt, successor, reportedPredecessor = i, w, pred
		}
	}

	if rolloverAt == 0 {
		t.Fatalf("no rollover across %d payments; the budget is %d",
			store.MaxSenderNonces+1, store.MaxSenderNonces)
	}
	// The changed identity must be reported, not discovered.
	if reportedPredecessor != firstWorkID {
		t.Fatalf("predecessor_work_id = %q; want the exhausted %q",
			reportedPredecessor, firstWorkID)
	}
	if successor == firstWorkID {
		t.Fatal("rollover reported a predecessor but did not change work_id")
	}

	// The exhausted identity is retired: a late payment on it is refused
	// rather than credited to a session nobody can draw on.
	stale := devModeCreateRequest(recipient, "roll-stale", baseURL)
	stale.Funding.FundedValueWei = &pb.BigUInt{Value: big.NewInt(2000).Bytes()}
	if created, err := payer.CreatePayment(ctx, stale); err == nil {
		if created.GetWorkId() == firstWorkID {
			t.Fatal("still minting against the retired identity")
		}
	}
}

// Two mints racing the boundary must produce ONE successor.
//
// Both see the exhausted rand and both ask the payee to rotate. If each
// got its own successor the route would fork: two live identities for
// one tuple, and a consumer with no way to order them.
func TestConcurrentBoundaryMintsProduceOneSuccessor(t *testing.T) {
	recipient := bytes20(0xf6)
	payee, cleanupPayee := defaultConfigReceiverStand(t, recipient)
	defer cleanupPayee()
	payer, baseURL, cleanupPayer := devModeSenderStand(t, payee)
	defer cleanupPayer()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	mint := func(id string) (*pb.CreatePaymentResponse, error) {
		req := devModeCreateRequest(recipient, id, baseURL)
		req.Funding.FundedValueWei = &pb.BigUInt{Value: big.NewInt(2000).Bytes()}
		return payer.CreatePayment(ctx, req)
	}

	// Walk the session right up to its last nonce, PROCESSING each
	// payment. Minting alone only moves the payer's watermark; the
	// payee's nonce ledger — the authoritative count, and the one
	// rotation keys on — advances when a payment is processed.
	first, err := mint("race-1")
	if err != nil {
		t.Fatal(err)
	}
	original := first.GetWorkId()
	if _, err := payee.OpenSession(ctx, &pb.OpenSessionRequest{
		WorkId:              original,
		Capability:          "openai:chat-completions",
		Offering:            "model-a",
		PricePerWorkUnitWei: big.NewInt(1000).Bytes(),
		WorkUnit:            "tokens",
	}); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	process := func(created *pb.CreatePaymentResponse) {
		t.Helper()
		if _, err := payee.ProcessPayment(ctx, &pb.ProcessPaymentRequest{
			PaymentBytes: created.GetPaymentBytes(),
			WorkId:       created.GetWorkId(),
		}); err != nil {
			t.Fatalf("ProcessPayment: %v", err)
		}
	}
	process(first)
	for i := 2; i <= store.MaxSenderNonces; i++ {
		created, err := mint(fmt.Sprintf("race-%d", i))
		if err != nil {
			t.Fatalf("priming mint %d: %v", i, err)
		}
		process(created)
	}

	// Now race several mints across the boundary.
	const racers = 8
	var wg sync.WaitGroup
	results := make([]string, racers)
	errs := make([]error, racers)
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func(i int) {
			defer wg.Done()
			resp, err := mint(fmt.Sprintf("race-boundary-%d", i))
			if err != nil {
				errs[i] = err
				return
			}
			results[i] = resp.GetWorkId()
		}(i)
	}
	wg.Wait()

	successors := map[string]int{}
	succeeded := 0
	for i, w := range results {
		if errs[i] != nil {
			// Failing closed is acceptable; forking is not.
			continue
		}
		succeeded++
		if w != original {
			successors[w]++
		}
	}
	// Without this the test passes when EVERY racer errored, proving
	// nothing about forking. At least one has to get through.
	if succeeded == 0 {
		t.Fatalf("every racer failed; nothing was proved about forking. errors: %v", errs)
	}
	if len(successors) == 0 {
		t.Fatalf("%d mints succeeded at the boundary and none rotated; the budget was spent",
			succeeded)
	}
	if len(successors) > 1 {
		t.Fatalf("boundary race forked the route into %d successors: %v",
			len(successors), successors)
	}
}

// Restart safety at the boundary.
//
// The previous version of this test only reopened a Bolt file and
// checked a number survived, which proves the store works and says
// nothing about rollover. This drives the real thing: spend the budget,
// restart the PAYEE, and confirm the next payment still rotates rather
// than being signed against an identity whose budget the restarted
// daemon still remembers.
func TestRolloverSurvivesAPayeeRestartAtTheBoundary(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rx.db")
	recipient := bytes20(0xf7)

	payee, stopPayee := receiverStandAt(t, recipient, dbPath)
	live := payee
	payer, baseURL, cleanupPayer := devModeSenderStandVia(t,
		func() pb.PayeeDaemonClient { return live })
	defer cleanupPayer()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	mint := func(id string) (*pb.CreatePaymentResponse, error) {
		req := devModeCreateRequest(recipient, id, baseURL)
		req.Funding.FundedValueWei = &pb.BigUInt{Value: big.NewInt(2000).Bytes()}
		return payer.CreatePayment(ctx, req)
	}

	first, err := mint("restart-1")
	if err != nil {
		t.Fatal(err)
	}
	original := first.GetWorkId()
	if _, err := payee.OpenSession(ctx, &pb.OpenSessionRequest{
		WorkId:              original,
		Capability:          "openai:chat-completions",
		Offering:            "model-a",
		PricePerWorkUnitWei: big.NewInt(1000).Bytes(),
		WorkUnit:            "tokens",
	}); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	// Spend the budget to its last nonce, PROCESSING each payment: the
	// payee's ledger is the authoritative count and only advances when a
	// payment is processed. Minting alone moves the payer's estimate,
	// and the payee is entitled to ignore that.
	process := func(created *pb.CreatePaymentResponse) {
		t.Helper()
		if _, err := live.ProcessPayment(ctx, &pb.ProcessPaymentRequest{
			PaymentBytes: created.GetPaymentBytes(),
			WorkId:       created.GetWorkId(),
		}); err != nil {
			t.Fatalf("ProcessPayment: %v", err)
		}
	}
	process(first)
	for i := 2; i <= store.MaxSenderNonces; i++ {
		created, err := mint(fmt.Sprintf("restart-%d", i))
		if err != nil {
			t.Fatalf("priming mint %d: %v", i, err)
		}
		process(created)
	}

	// Restart the payee on the SAME database, and point the sender at
	// the new one — otherwise this only proves a closed connection
	// fails, which is not what restart safety means.
	stopPayee()
	payee2, stopPayee2 := receiverStandAt(t, recipient, dbPath)
	defer stopPayee2()
	live = payee2

	// The next mint must roll over, not sign against the spent identity.
	next, err := mint("restart-boundary")
	if err != nil {
		t.Fatalf("boundary mint after a payee restart: %v", err)
	}
	if next.GetWorkId() == original {
		t.Fatal("after a payee restart the boundary mint reused the exhausted identity; " +
			"a restarted daemon that forgets its budget signs payments it will refuse")
	}
	if next.GetPredecessorWorkId() != original {
		t.Fatalf("predecessor_work_id = %q; want the exhausted %q",
			next.GetPredecessorWorkId(), original)
	}
}

// receiverStandAt is defaultConfigReceiverStand with a caller-chosen
// database path, so a test can stop a payee and start a new one on the
// same state — which is what "restart" has to mean here.
func receiverStandAt(t *testing.T, recipient []byte, dbPath string) (pb.PayeeDaemonClient, func()) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	svc := receiver.New(st, receiver.Config{Recipient: recipient}, nil)

	sockPath := filepath.Join(t.TempDir(), "rx.sock")
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
	stopped := false
	return pb.NewPayeeDaemonClient(conn), func() {
		if stopped {
			return
		}
		stopped = true
		_ = conn.Close()
		gs.GracefulStop()
		_ = st.Close()
	}
}

// Partial payer state loss is NOT yet recoverable, and this pins the
// boundary of what is.
//
// A payer that loses its durable watermark restarts its nonce stream
// low. Every nonce it then produces has already been seen, so it is
// refused NONCE_REPLAY, credits nothing, and cannot make progress on
// that rand again — the exact failure the durable watermark exists to
// prevent, reached by losing the watermark rather than never having had
// one. The nonce-cap rotation does not cover it: the cap is about a
// stream that ran too far, this is one that ran backwards.
//
// It is not fixable by guessing from a single payment. A re-delivered
// early payment replays a low nonce exactly as a rewound sender does, so
// any positional rule rotates the route on ordinary retries. This test
// records both halves so the distinction does not get lost: a duplicate
// delivery stays a plain replay, and a rewound payer is currently stuck.
// The fix is for the payee to report its high-water nonce so the payer
// resyncs deliberately — lnm-nbx.
func TestDuplicateDeliveryStaysAReplayAndARewoundPayerIsStuck(t *testing.T) {
	recipient := bytes20(0xf8)
	payee, cleanupPayee := defaultConfigReceiverStand(t, recipient)
	defer cleanupPayee()
	payer, baseURL, payerStore, cleanupPayer := devModeSenderStandWithStore(t,
		func() pb.PayeeDaemonClient { return payee })
	defer cleanupPayer()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	mint := func(id string) *pb.CreatePaymentResponse {
		t.Helper()
		req := devModeCreateRequest(recipient, id, baseURL)
		req.Funding.FundedValueWei = &pb.BigUInt{Value: big.NewInt(2000).Bytes()}
		created, err := payer.CreatePayment(ctx, req)
		if err != nil {
			t.Fatalf("mint %s: %v", id, err)
		}
		return created
	}
	process := func(created *pb.CreatePaymentResponse) *pb.ProcessPaymentResponse {
		t.Helper()
		resp, err := payee.ProcessPayment(ctx, &pb.ProcessPaymentRequest{
			PaymentBytes: created.GetPaymentBytes(),
			WorkId:       created.GetWorkId(),
		})
		if err != nil {
			t.Fatalf("ProcessPayment: %v", err)
		}
		return resp
	}

	first := mint("loss-1")
	workID := first.GetWorkId()
	if _, err := payee.OpenSession(ctx, &pb.OpenSessionRequest{
		WorkId:              workID,
		Capability:          "openai:chat-completions",
		Offering:            "model-a",
		PricePerWorkUnitWei: big.NewInt(1000).Bytes(),
		WorkUnit:            "tokens",
	}); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	process(first)
	for i := 2; i <= 20; i++ {
		process(mint(fmt.Sprintf("loss-%d", i)))
	}

	// A re-sent payment is a plain replay. It must NOT rotate the route:
	// a gateway retrying delivery would otherwise churn work_id every
	// time, and every consumer keying evidence on it would follow.
	dup := process(first)
	if dup.GetDominantRejection() != pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_NONCE_REPLAY {
		t.Fatalf("duplicate delivery reported %s; a re-sent payment is a plain replay",
			dup.GetDominantRejection())
	}
	if credited := new(big.Int).SetBytes(dup.GetCreditedEv()); credited.Sign() != 0 {
		t.Fatalf("a duplicate delivery credited %s a second time", credited)
	}

	// A rewound payer is currently stuck, and stuck LOUDLY — refused
	// rather than silently credited nothing on a payment that looks
	// successful.
	if err := payerStore.ForgetSenderNonces(workID); err != nil {
		t.Fatalf("simulating payer state loss: %v", err)
	}
	stuck := process(mint("loss-after-wipe"))
	if stuck.GetTicketsRejected() == 0 {
		t.Fatal("a rewound payer's payment was accepted; its nonces were already spent")
	}
	if credited := new(big.Int).SetBytes(stuck.GetCreditedEv()); credited.Sign() != 0 {
		t.Fatalf("a rewound payer's payment credited %s", credited)
	}
}
