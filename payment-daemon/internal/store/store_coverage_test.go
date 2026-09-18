package store

import (
	"bytes"
	"errors"
	"math/big"
	"testing"
	"time"
)

func TestMintReservationReplayAndEviction(t *testing.T) {
	st := openTestStore(t)
	sender := bytes20(0x11)
	fingerprint := []byte("request-v1")
	if got, err := st.MintReserve(sender, "mint-1", fingerprint); err != nil || got != nil {
		t.Fatalf("initial reserve=%+v err=%v", got, err)
	}
	if _, err := st.MintReserve(sender, "mint-1", fingerprint); !errors.Is(err, ErrMintIncomplete) {
		t.Fatalf("second reserve err=%v", err)
	}
	if _, err := st.MintRecall(sender, "mint-1", []byte("different")); !errors.Is(err, ErrMintFingerprintMismatch) {
		t.Fatalf("mismatched recall err=%v", err)
	}
	rec := MintRecord{Fingerprint: fingerprint, PaymentBytes: []byte("payment"), TicketsCreated: 2, ExpectedValue: []byte{3}, WorkID: "work-1"}
	if err := st.MintRecord(sender, "mint-1", rec); err != nil {
		t.Fatal(err)
	}
	for _, replay := range []func() (*MintRecord, error){
		func() (*MintRecord, error) { return st.MintRecall(sender, "mint-1", fingerprint) },
		func() (*MintRecord, error) { return st.MintReserve(sender, "mint-1", fingerprint) },
	} {
		got, err := replay()
		if err != nil || got.WorkID != "work-1" || !bytes.Equal(got.PaymentBytes, []byte("payment")) {
			t.Fatalf("replay=%+v err=%v", got, err)
		}
	}
	if got, err := st.MintRecall(bytes20(0x12), "mint-1", fingerprint); err != nil || got != nil {
		t.Fatalf("sender scoping failed: %+v %v", got, err)
	}
	evicted, err := st.EvictMints(time.Now().Add(time.Hour))
	if err != nil || evicted != 1 {
		t.Fatalf("evicted=%d err=%v", evicted, err)
	}
	if _, err := st.MintRecall(sender, "mint-1", fingerprint); !errors.Is(err, ErrMintExpired) {
		t.Fatalf("expired recall err=%v", err)
	}
	if _, err := st.MintReserve(sender, "mint-1", fingerprint); !errors.Is(err, ErrMintExpired) {
		t.Fatalf("expired reserve err=%v", err)
	}
	if n, err := st.EvictMints(time.Time{}); err != nil || n != 0 {
		t.Fatalf("empty eviction=%d err=%v", n, err)
	}
}

func TestReceiverNonceLedgerAndSenderWatermark(t *testing.T) {
	st := openTestStore(t)
	rand := big.NewInt(12345)
	seen, err := st.NonceSeen(rand, 7)
	if err != nil || seen {
		t.Fatalf("initial seen=%v err=%v", seen, err)
	}
	if err := st.RecordNonce(rand, 7); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordNonce(rand, 7); !errors.Is(err, ErrNonceAlreadySeen) {
		t.Fatalf("duplicate nonce err=%v", err)
	}
	if count, err := st.NonceCount(rand); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if highest, found, err := st.HighestSenderNonce(rand); err != nil || !found || highest != 7 {
		t.Fatalf("highest=%d found=%v err=%v", highest, found, err)
	}
	if err := st.FillNonceLedger(rand, 8, MaxSenderNonces-1); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordNonce(rand, 9999); !errors.Is(err, ErrTooManyNonces) {
		t.Fatalf("nonce cap err=%v", err)
	}
	if highest, found, err := st.HighestSenderNonce(big.NewInt(999)); err != nil || found || highest != 0 {
		t.Fatalf("empty highest=%d found=%v err=%v", highest, found, err)
	}

	for _, fn := range []func() error{
		func() error { _, err := st.NextSenderNonce(""); return err },
		func() error { _, err := st.SenderNoncesUsed(""); return err },
		func() error { return st.ForgetSenderNonces("") },
		func() error { _, err := st.ResyncSenderNonces("", 1); return err },
	} {
		if err := fn(); err == nil {
			t.Fatal("empty work ID accepted")
		}
	}
	if used, err := st.SenderNoncesUsed("work"); err != nil || used != 0 {
		t.Fatalf("initial watermark=%d err=%v", used, err)
	}
	if raised, err := st.ResyncSenderNonces("work", 10); err != nil || !raised {
		t.Fatalf("resync raised=%v err=%v", raised, err)
	}
	if raised, err := st.ResyncSenderNonces("work", 5); err != nil || raised {
		t.Fatalf("lower resync raised=%v err=%v", raised, err)
	}
	if next, err := st.NextSenderNonce("work"); err != nil || next != 11 {
		t.Fatalf("next=%d err=%v", next, err)
	}
	if err := st.ForgetSenderNonces("work"); err != nil {
		t.Fatal(err)
	}
	if used, _ := st.SenderNoncesUsed("work"); used != 0 {
		t.Fatalf("forgotten watermark=%d", used)
	}
}

func TestRedemptionQueueLifecycle(t *testing.T) {
	st := openTestStore(t)
	hash := hash32(0x44)
	ticket := &SignedTicket{Sender: bytes20(1), FaceValue: big.NewInt(123), CreationRound: 8}
	if ok, err := st.EnqueueRedemption([]byte{1}, ticket); err == nil || ok {
		t.Fatalf("short hash accepted: ok=%v err=%v", ok, err)
	}
	if ok, err := st.EnqueueRedemption(hash, nil); err == nil || ok {
		t.Fatalf("nil ticket accepted: ok=%v err=%v", ok, err)
	}
	if ok, err := st.EnqueueRedemption(hash, ticket); err != nil || !ok {
		t.Fatalf("enqueue=%v err=%v", ok, err)
	}
	if ok, err := st.EnqueueRedemption(hash, ticket); err != nil || ok {
		t.Fatalf("duplicate enqueue=%v err=%v", ok, err)
	}
	pending, err := st.PendingRedemptions()
	if err != nil || len(pending) != 1 || pending[0].Seq != 1 || !bytes.Equal(pending[0].Hash, hash) {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if tx, err := st.RedeemedTxHash(hash); err != nil || tx != nil {
		t.Fatalf("pre-redemption tx=%x err=%v", tx, err)
	}
	if err := st.MarkRedeemed([]byte{1}, nil, ticket, 9); err == nil {
		t.Fatal("short redeemed hash accepted")
	}
	if err := st.MarkRedeemed(hash, nil, nil, 9); err == nil {
		t.Fatal("nil redeemed ticket accepted")
	}
	txHash := hash32(0x55)
	if err := st.MarkRedeemed(hash, txHash, ticket, 9); err != nil {
		t.Fatal(err)
	}
	if got, err := st.RedeemedTxHash(hash); err != nil || !bytes.Equal(got, txHash) {
		t.Fatalf("redeemed tx=%x err=%v", got, err)
	}
	if pending, _ := st.PendingRedemptions(); len(pending) != 0 {
		t.Fatalf("pending after redemption=%+v", pending)
	}
	if ok, err := st.EnqueueRedemption(hash, ticket); err != nil || ok {
		t.Fatalf("redeemed ticket requeued=%v err=%v", ok, err)
	}
}

func TestFundWholesaleMovesOnlyAvailableGenerationBalance(t *testing.T) {
	st := openTestStore(t)
	payer, payee := bytes20(0xa1), bytes20(0xb1)
	now := time.Now().UTC()
	if _, _, err := st.FundWholesale(nil, payee, "work", now); err == nil {
		t.Fatal("invalid principals accepted")
	}
	if _, _, err := st.FundWholesale(payer, payee, "missing", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing generation err=%v", err)
	}
	fundLegacySession(t, st, payer, "generation", 321)
	account, moved, err := st.FundWholesale(payer, payee, "generation", now)
	if err != nil || moved.Int64() != 321 || account.CreditedWei != "321" || account.Available().Int64() != 321 {
		t.Fatalf("account=%+v moved=%v err=%v", account, moved, err)
	}
	again, moved, err := st.FundWholesale(payer, payee, "generation", now.Add(time.Minute))
	if err != nil || moved.Sign() != 0 || again.CreditedWei != "321" {
		t.Fatalf("repeat account=%+v moved=%v err=%v", again, moved, err)
	}
	loaded, err := st.GetWholesaleAccount(payer, payee)
	if err != nil || loaded.CreditedWei != "321" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
}
