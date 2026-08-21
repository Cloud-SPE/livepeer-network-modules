package payment

import (
	"context"
	"errors"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
	"math/big"
	"testing"
)

// The mock stands in for a real payee daemon in every unit test,
// conformance run and dev deployment. Each test here pins a behavior
// whose ABSENCE previously let a production defect pass unnoticed — so
// what they really guard is the mock's ability to fail the way the real
// ledger fails.
//
// Every one of these corresponds to a defect that shipped and was found
// on Arbitrum One, not in CI.

// TestMockReproducesTheZeroBillingSequence: GetTicketParams must create
// the payment session BEFORE any offering has priced it, because a
// sender cannot mint without params. The mock used to create nothing
// here, so the ordering that made every exchange bill zero could not be
// represented in a test.
func TestMockReproducesTheZeroBillingSequence(t *testing.T) {
	m := NewMock()
	ctx := context.Background()

	tp, err := m.GetTicketParams(ctx, GetTicketParamsRequest{
		Sender: []byte("sender-20-bytes-0000"), Recipient: []byte("recipient-20-bytes00"),
		FaceValue: big.NewInt(1000), Capability: "cap", Offering: "off",
	})
	if err != nil {
		t.Fatal(err)
	}
	workID := hexOf(tp.RecipientRandHash)

	// Billing before an offering prices it must FAIL, not charge zero.
	if _, err := m.DebitBalance(ctx, DebitBalanceRequest{
		Sender: []byte("sender-20-bytes-0000"), WorkID: workID, WorkUnits: 42, DebitSeq: 1,
	}); !errors.Is(err, ErrPricingUnset) {
		t.Fatalf("debit on an unpriced session = %v; want ErrPricingUnset — "+
			"treating unset as zero is how work gets served free", err)
	}

	// The broker's open supplies the price; it must stick.
	if _, err := m.OpenSession(ctx, OpenSessionRequest{
		WorkID: workID, Capability: "cap", Offering: "off",
		PricePerWorkUnitWei: big.NewInt(100), PerUnits: 1000, WorkUnit: "tokens",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ProcessPayment(ctx, ProcessPaymentRequest{
		WorkID: workID, PaymentBytes: []byte("stub"),
	}); err != nil {
		t.Fatal(err)
	}
	res, err := m.DebitBalance(ctx, DebitBalanceRequest{
		Sender: senderOfSession(m, workID), WorkID: workID, WorkUnits: 42, DebitSeq: 1,
	})
	if err != nil {
		t.Fatalf("debit after pricing: %v", err)
	}
	if res.DebitedWei.Sign() == 0 {
		t.Fatal("a priced session charged nothing")
	}
}

// TestMockRefusesToRePriceALiveSession: re-pricing bills already-funded
// work at a rate the payer never agreed to.
func TestMockRefusesToRePriceALiveSession(t *testing.T) {
	m := NewMock()
	ctx := context.Background()
	open := func(price int64) error {
		_, err := m.OpenSession(ctx, OpenSessionRequest{
			WorkID: "w1", Capability: "c", Offering: "o", WorkUnit: "tokens",
			PricePerWorkUnitWei: big.NewInt(price), PerUnits: 1,
		})
		return err
	}
	if err := open(10); err != nil {
		t.Fatal(err)
	}
	if err := open(10); err != nil {
		t.Fatalf("re-opening at the same price must be idempotent: %v", err)
	}
	if err := open(99); !errors.Is(err, ErrPricingConflict) {
		t.Fatalf("re-price = %v; want ErrPricingConflict", err)
	}
}

// TestMockBillsCumulatively: the second exchange on a session costs the
// difference of two ceilings. A mock charging an independent ceiling
// each time would agree with a broker that did the same, and both would
// disagree with the real ledger.
func TestMockBillsCumulatively(t *testing.T) {
	m := NewMock()
	ctx := context.Background()
	if _, err := m.OpenSession(ctx, OpenSessionRequest{
		WorkID: "w1", Capability: "c", Offering: "o", WorkUnit: "tokens",
		PricePerWorkUnitWei: big.NewInt(100), PerUnits: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ProcessPayment(ctx, ProcessPaymentRequest{WorkID: "w1", PaymentBytes: []byte("stub")}); err != nil {
		t.Fatal(err)
	}
	sender := senderOfSession(m, "w1")
	charges := []int64{}
	for seq := uint64(1); seq <= 3; seq++ {
		res, err := m.DebitBalance(ctx, DebitBalanceRequest{
			Sender: sender, WorkID: "w1", WorkUnits: 42, DebitSeq: seq,
		})
		if err != nil {
			t.Fatal(err)
		}
		charges = append(charges, res.DebitedWei.Int64())
	}
	// 42 units at 100 per 1000: ceil(4.2)=5, then 9-5=4, then 13-9=4.
	if charges[0] != 5 || charges[1] != 4 || charges[2] != 4 {
		t.Fatalf("charges = %v; want [5 4 4] — independent ceilings would give [5 5 5]", charges)
	}
}

// TestMockRefusesDebitsOnAClosedSession: a closed session credits but
// cannot be billed. That asymmetry is what made a broker closing a
// SHARED identity serve work free from the second exchange onward.
func TestMockRefusesDebitsOnAClosedSession(t *testing.T) {
	m := NewMock()
	ctx := context.Background()
	if _, err := m.OpenSession(ctx, OpenSessionRequest{
		WorkID: "w1", Capability: "c", Offering: "o", WorkUnit: "tokens",
		PricePerWorkUnitWei: big.NewInt(1), PerUnits: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ProcessPayment(ctx, ProcessPaymentRequest{WorkID: "w1", PaymentBytes: []byte("stub")}); err != nil {
		t.Fatal(err)
	}
	sender := senderOfSession(m, "w1")
	if err := m.CloseSession(ctx, sender, "w1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DebitBalance(ctx, DebitBalanceRequest{
		Sender: sender, WorkID: "w1", WorkUnits: 1, DebitSeq: 1,
	}); err == nil {
		t.Fatal("a closed session accepted a debit; the real ledger refuses it")
	}
}

// TestMockRecoversTheSenderFromThePayment: one wallet minting against
// two ticket identities is a rotation, not two wallets. Deriving the
// sender from the whole payment made a rebind fail a sender check that
// should have passed.
func TestMockRecoversTheSenderFromThePayment(t *testing.T) {
	wallet := []byte("0123456789abcdef0123")
	a := stubSenderFromPayment(paymentWithSender(t, wallet, "rand-a"))
	b := stubSenderFromPayment(paymentWithSender(t, wallet, "rand-b"))
	if string(a) != string(b) {
		t.Fatalf("same wallet, two ticket identities gave senders %x and %x", a, b)
	}
	if string(a) != string(wallet) {
		t.Fatalf("sender = %x; want the payment's own %x", a, wallet)
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0x0f])
	}
	return string(out)
}

func senderOfSession(m *Mock, workID string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[workID]; ok {
		return append([]byte(nil), s.sender...)
	}
	return nil
}

func paymentWithSender(t *testing.T, wallet []byte, rand string) []byte {
	t.Helper()
	raw, err := proto.Marshal(&pb.Payment{
		Sender:       wallet,
		TicketParams: &pb.TicketParams{RecipientRandHash: []byte(rand)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A payment to a closed session must be REFUSED, not credited.
//
// This is how a payee retires an identity: the old session is closed and
// its index dropped so the next params issuance mints a fresh work_id. A
// payer that hasn't learned of the rotation keeps paying the old one. The
// mock used to credit those payments while DebitBalance refused them, so
// a broker that stranded a payer's funds on a dead identity — real ETH
// in, no work ever billable out — passed every mock-backed test.
func TestMockProcessPaymentRefusesClosedSession(t *testing.T) {
	m := NewMock()
	ctx := context.Background()
	sender := []byte("sender-20-bytes-0000")
	tp, err := m.GetTicketParams(ctx, GetTicketParamsRequest{
		Sender: sender, Recipient: []byte("recipient-20-bytes00"),
		FaceValue: big.NewInt(1000), Capability: "c", Offering: "o",
	})
	if err != nil {
		t.Fatal(err)
	}
	workID := hexOf(tp.RecipientRandHash)
	if _, err := m.OpenSession(ctx, OpenSessionRequest{
		WorkID: workID, Capability: "c", Offering: "o",
		PricePerWorkUnitWei: big.NewInt(100), PerUnits: 1000, WorkUnit: "tokens",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ProcessPayment(ctx, ProcessPaymentRequest{
		WorkID: workID, PaymentBytes: []byte("stub"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.CloseSession(ctx, senderOfSession(m, workID), workID); err != nil {
		t.Fatal(err)
	}

	before := balanceOfSession(m, workID)
	res, err := m.ProcessPayment(ctx, ProcessPaymentRequest{
		WorkID: workID, PaymentBytes: []byte("stub"),
	})
	if err != nil {
		t.Fatalf("ProcessPayment on a closed session returned a transport error %v; it must "+
			"return the refusal in band so the broker sees the rotation signal", err)
	}
	if res.CreditedEV.Sign() != 0 {
		t.Fatalf("credited %s to a closed session; a closed session takes no more money",
			res.CreditedEV)
	}
	if after := balanceOfSession(m, workID); after.Cmp(before) != 0 {
		t.Fatalf("balance moved %s -> %s on a closed session", before, after)
	}
	if res.TicketsRejected == 0 {
		t.Fatal("tickets_rejected is 0; the broker reads that as accepted and never rebinds")
	}
	// The broker only calls a batch fully rejected when the count covers
	// every status entry — a bare count with no statuses reads as accepted.
	if len(res.TicketStatus) == 0 || int(res.TicketsRejected) < len(res.TicketStatus) {
		t.Fatalf("tickets_rejected=%d over %d statuses; the broker needs every entry rejected "+
			"to raise recipient_rotated", res.TicketsRejected, len(res.TicketStatus))
	}
	if res.DominantRejection != PaymentRejectionReasonInvalidRecipientRand {
		t.Fatalf("dominant rejection = %v; want INVALID_RECIPIENT_RAND, the signal that "+
			"drives the payer to re-fetch params and rebind", res.DominantRejection)
	}
}

func balanceOfSession(m *Mock, workID string) *big.Int {
	for _, r := range m.Sessions() {
		if r.WorkID == workID {
			return r.Balance
		}
	}
	return big.NewInt(0)
}
