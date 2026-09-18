package txintent_test

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/config"
	cerrors "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/errors"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/receipts"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"
	chaintest "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

type harness struct {
	rpc      *chaintest.FakeRPC
	keystore *chaintest.FakeKeystore
	gas      *chaintest.FakeGasOracle
	receipts *chaintest.FakeReceipts
	store    *txintent.Manager
	logger   *chaintest.FakeLogger
	metrics  *chaintest.FakeMetrics

	sentMu  sync.Mutex
	sentTxs []*ethtypes.Transaction
	onSend  func(*ethtypes.Transaction)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		rpc:      chaintest.NewFakeRPC(),
		keystore: chaintest.NewFakeKeystore("test-orch"),
		gas:      chaintest.NewFakeGasOracle(),
		receipts: chaintest.NewFakeReceipts(),
		logger:   chaintest.NewFakeLogger(),
		metrics:  chaintest.NewFakeMetrics(),
	}

	// Capture every broadcast so tests can assert on tx hashes.
	h.rpc.SendTransactionFunc = func(_ context.Context, tx *ethtypes.Transaction) error {
		h.sentMu.Lock()
		h.sentTxs = append(h.sentTxs, tx)
		h.sentMu.Unlock()
		if h.onSend != nil {
			h.onSend(tx)
		}
		return nil
	}

	policy := config.Default().TxIntent
	policy.SubmitTimeout = 1 * time.Second
	policy.MaxReplacements = 2

	proc, err := txintent.NewDefaultProcessor(txintent.ProcessorConfig{
		Policy:             policy,
		ChainID:            42161,
		ReorgConfirmations: 4,
		GasLimit:           500_000,
		RPC:                h.rpc,
		Keystore:           h.keystore,
		Gas:                h.gas,
		Receipts:           h.receipts,
		Clock:              clock.System(),
		Logger:             h.logger,
		Metrics:            h.metrics,
	})
	if err != nil {
		t.Fatalf("NewDefaultProcessor: %v", err)
	}

	mgr, err := txintent.New(policy, chaintest.NewFakeStore(), clock.System(), h.logger, h.metrics, proc)
	if err != nil {
		t.Fatalf("Manager.New: %v", err)
	}
	h.store = mgr
	return h
}

func (h *harness) sentTxHashes() []chain.TxHash {
	h.sentMu.Lock()
	defer h.sentMu.Unlock()
	out := make([]chain.TxHash, 0, len(h.sentTxs))
	for _, tx := range h.sentTxs {
		out = append(out, tx.Hash())
	}
	return out
}

func sampleParams(kind string) txintent.Params {
	return txintent.Params{
		Kind:      kind,
		KeyParams: []byte(kind),
		To:        chain.Address{0x01},
		CallData:  []byte{0xde, 0xad, 0xbe, 0xef},
		Value:     big.NewInt(0),
		GasLimit:  500_000,
	}
}

func TestProcessor_HappyPath(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.onSend = func(tx *ethtypes.Transaction) {
		h.receipts.Set(tx.Hash(), &receipts.Receipt{
			TxHash:    tx.Hash(),
			Status:    1,
			Confirmed: true,
		})
	}

	id, err := h.store.Submit(ctx, sampleParams("InitializeRound"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	final, err := h.store.Wait(waitCtx, id)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if final.Status != txintent.StatusConfirmed {
		t.Errorf("Status = %s, want confirmed", final.Status)
	}

	if got := h.metrics.CounterValue(
		"livepeer_chain_txintent_terminal_total",
		metrics.Labels{"kind": "InitializeRound", "outcome": "confirmed"},
	); got != 1 {
		t.Errorf("confirmed counter = %v, want 1", got)
	}
}

func TestProcessor_RevertedTransitionsToFailed(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.onSend = func(tx *ethtypes.Transaction) {
		h.receipts.Set(tx.Hash(), &receipts.Receipt{
			TxHash: tx.Hash(),
			Status: 0, // reverted
		})
	}

	id, _ := h.store.Submit(ctx, sampleParams("BadCall"))

	waitCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	final, err := h.store.Wait(waitCtx, id)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if final.Status != txintent.StatusFailed {
		t.Errorf("Status = %s, want failed", final.Status)
	}
	if final.FailedReason == nil || final.FailedReason.Class != cerrors.ClassReverted {
		t.Errorf("FailedReason = %+v, want ClassReverted", final.FailedReason)
	}
}

func TestProcessor_NoncePastExhaustsFreshNonceRetriesThenFails(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	var sends int32
	h.rpc.SendTransactionFunc = func(_ context.Context, _ *ethtypes.Transaction) error {
		atomic.AddInt32(&sends, 1)
		return errors.New("nonce too low")
	}

	id, _ := h.store.Submit(ctx, sampleParams("NonceCollision"))

	waitCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	final, err := h.store.Wait(waitCtx, id)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if final.Status != txintent.StatusFailed {
		t.Errorf("Status = %s, want failed", final.Status)
	}
	if final.FailedReason == nil || final.FailedReason.Class != cerrors.ClassNoncePast {
		t.Errorf("FailedReason = %+v, want ClassNoncePast", final.FailedReason)
	}
	// A persistent nonce-too-low should have been retried with fresh nonces
	// before giving up, not failed on the first rejection.
	if got := atomic.LoadInt32(&sends); got < 2 {
		t.Errorf("broadcast attempts = %d, want ≥2 (fresh-nonce retries)", got)
	}
}

func TestProcessor_ConcurrentSubmitsGetDistinctNonces(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Simulate a lagging node: the pending nonce it reports never advances,
	// the way it wouldn't in the instant between two back-to-back submits.
	h.rpc.PendingNonceAtFunc = func(_ context.Context, _ chain.Address) (uint64, error) {
		return 100, nil
	}
	h.onSend = func(tx *ethtypes.Transaction) {
		h.receipts.Set(tx.Hash(), &receipts.Receipt{TxHash: tx.Hash(), Status: 1, Confirmed: true})
	}

	id1, err := h.store.Submit(ctx, sampleParams("TransferBond"))
	if err != nil {
		t.Fatalf("Submit 1: %v", err)
	}
	id2, err := h.store.Submit(ctx, sampleParams("WithdrawFees"))
	if err != nil {
		t.Fatalf("Submit 2: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for _, id := range []txintent.IntentID{id1, id2} {
		final, err := h.store.Wait(waitCtx, id)
		if err != nil {
			t.Fatalf("Wait(%s): %v", id.Hex(), err)
		}
		if final.Status != txintent.StatusConfirmed {
			t.Errorf("intent %s Status = %s, want confirmed", id.Hex(), final.Status)
		}
	}

	h.sentMu.Lock()
	defer h.sentMu.Unlock()
	seen := map[uint64]bool{}
	for _, tx := range h.sentTxs {
		if seen[tx.Nonce()] {
			t.Fatalf("nonce %d assigned to two broadcasts; sent nonces so far: %v", tx.Nonce(), seen)
		}
		seen[tx.Nonce()] = true
	}
	if !seen[100] || !seen[101] {
		t.Errorf("expected nonces 100 and 101 to be assigned, got %v", seen)
	}
}

func TestProcessor_NonceTooLowFirstBroadcastRetriesWithFreshNonce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// The node's nonce advances between our fetch and our broadcast (an
	// external tx landed): the first send is rejected, the refetch sees the
	// new nonce, and the retry succeeds.
	var fetches int32
	h.rpc.PendingNonceAtFunc = func(_ context.Context, _ chain.Address) (uint64, error) {
		if atomic.AddInt32(&fetches, 1) == 1 {
			return 5, nil
		}
		return 7, nil
	}
	h.rpc.SendTransactionFunc = func(_ context.Context, tx *ethtypes.Transaction) error {
		if tx.Nonce() < 7 {
			return errors.New("nonce too low")
		}
		h.receipts.Set(tx.Hash(), &receipts.Receipt{TxHash: tx.Hash(), Status: 1, Confirmed: true})
		return nil
	}

	id, _ := h.store.Submit(ctx, sampleParams("RacedByExternalTx"))

	waitCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	final, err := h.store.Wait(waitCtx, id)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if final.Status != txintent.StatusConfirmed {
		t.Fatalf("Status = %s, want confirmed", final.Status)
	}
	last := final.CurrentAttempt()
	if last == nil || last.Nonce != 7 {
		t.Errorf("final attempt nonce = %+v, want 7", last)
	}
}

func TestProcessor_RebroadcastNoncePastResolvesViaReceipt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// First broadcast dies transiently after the attempt is persisted (the
	// signed-but-not-submitted crash window). The rebroadcast of the same
	// signed tx is rejected nonce-too-low because the original actually
	// reached the node and mined — the intent must resolve confirmed via
	// its receipt, not terminally fail.
	var sends int32
	h.rpc.SendTransactionFunc = func(_ context.Context, tx *ethtypes.Transaction) error {
		switch atomic.AddInt32(&sends, 1) {
		case 1:
			h.receipts.Set(tx.Hash(), &receipts.Receipt{TxHash: tx.Hash(), Status: 1, Confirmed: true})
			return errors.New("connection reset by peer")
		default:
			return errors.New("nonce too low")
		}
	}

	id, _ := h.store.Submit(ctx, sampleParams("CrashedMidBroadcast"))

	waitCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	final, err := h.store.Wait(waitCtx, id)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if final.Status != txintent.StatusConfirmed {
		t.Errorf("Status = %s, want confirmed (FailedReason: %+v)", final.Status, final.FailedReason)
	}
}

func TestProcessor_InsufficientFundsIsTerminal(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.rpc.SendTransactionFunc = func(_ context.Context, _ *ethtypes.Transaction) error {
		return errors.New("insufficient funds for gas * price + value")
	}

	id, _ := h.store.Submit(ctx, sampleParams("BroadcastFails"))

	waitCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	final, _ := h.store.Wait(waitCtx, id)
	if final.Status != txintent.StatusFailed {
		t.Errorf("Status = %s, want failed", final.Status)
	}
	if final.FailedReason == nil || final.FailedReason.Class != cerrors.ClassInsufficientFunds {
		t.Errorf("FailedReason class = %v, want ClassInsufficientFunds", final.FailedReason)
	}
}

func TestProcessor_ReplacementOnTimeout(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	id, _ := h.store.Submit(ctx, sampleParams("Slow"))

	// Wait for the first broadcast, then DON'T set a receipt — let the
	// SubmitTimeout expire so the processor replaces.
	deadline := time.After(10 * time.Second)
	for len(h.sentTxHashes()) < 2 {

		select {
		case <-deadline:
			t.Fatalf("expected at least 2 broadcasts (original + replacement), got %d", len(h.sentTxHashes()))
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Confirm the second (replacement) tx.
	hashes := h.sentTxHashes()
	h.receipts.Set(hashes[1], &receipts.Receipt{TxHash: hashes[1], Status: 1, Confirmed: true})

	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	final, err := h.store.Wait(waitCtx, id)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if final.Status != txintent.StatusConfirmed {
		t.Errorf("Status = %s, want confirmed", final.Status)
	}
	if len(final.Attempts) < 2 {
		t.Errorf("expected ≥2 attempts post-replacement, got %d", len(final.Attempts))
	}
	if final.Attempts[0].ReplacedAt == nil {
		t.Errorf("first attempt should be marked replaced")
	}
	// Bumped fee
	if final.Attempts[1].GasFeeCap.Cmp(final.Attempts[0].GasFeeCap) <= 0 {
		t.Errorf("replacement should have higher GasFeeCap")
	}
}

func TestProcessor_ReplacementExhaustionFailsTransient(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	id, _ := h.store.Submit(ctx, sampleParams("NeverConfirms"))

	// Wait long enough for SubmitTimeout * (MaxReplacements+1) to elapse
	// without a receipt.
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	final, err := h.store.Wait(waitCtx, id)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if final.Status != txintent.StatusFailed {
		t.Errorf("Status = %s, want failed", final.Status)
	}
	if final.FailedReason == nil || final.FailedReason.Class != cerrors.ClassTransient {
		t.Errorf("FailedReason class = %v, want ClassTransient", final.FailedReason)
	}
	if final.FailedReason != nil && final.FailedReason.Code != "tx.replacement_exhausted" {
		t.Errorf("FailedReason code = %q, want tx.replacement_exhausted", final.FailedReason.Code)
	}
}

func TestProcessor_ReorgResubmits(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	var broadcastCount int
	h.onSend = func(tx *ethtypes.Transaction) {
		broadcastCount++
		switch broadcastCount {
		case 1:
			h.receipts.Set(tx.Hash(), &receipts.Receipt{
				TxHash:  tx.Hash(),
				Reorged: true,
			})
		case 2:
			h.receipts.Set(tx.Hash(), &receipts.Receipt{
				TxHash:    tx.Hash(),
				Status:    1,
				Confirmed: true,
			})
		}
	}

	id, _ := h.store.Submit(ctx, sampleParams("ReorgRecovery"))

	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	final, _ := h.store.Wait(waitCtx, id)
	if final.Status != txintent.StatusConfirmed {
		t.Errorf("Status = %s, want confirmed", final.Status)
	}
}

func TestNewDefaultProcessor_RequiresDeps(t *testing.T) {
	policy := config.Default().TxIntent
	cases := []struct {
		name string
		mut  func(*txintent.ProcessorConfig)
	}{
		{"no rpc", func(c *txintent.ProcessorConfig) { c.RPC = nil }},
		{"no keystore", func(c *txintent.ProcessorConfig) { c.Keystore = nil }},
		{"no gas", func(c *txintent.ProcessorConfig) { c.Gas = nil }},
		{"no receipts", func(c *txintent.ProcessorConfig) { c.Receipts = nil }},
		{"no chainID", func(c *txintent.ProcessorConfig) { c.ChainID = 0 }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := txintent.ProcessorConfig{
				Policy:   policy,
				ChainID:  42161,
				RPC:      chaintest.NewFakeRPC(),
				Keystore: chaintest.NewFakeKeystore("seed"),
				Gas:      chaintest.NewFakeGasOracle(),
				Receipts: chaintest.NewFakeReceipts(),
			}
			tt.mut(&c)
			if _, err := txintent.NewDefaultProcessor(c); err == nil {
				t.Errorf("NewDefaultProcessor with %s should fail", tt.name)
			}
		})
	}
}

func TestProcessor_AdoptedAttemptWithoutCapsReplacesFromOracle(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Adopted with no gas caps. No receipt is ever set for the adopted
	// hash, so SubmitTimeout expires and the processor must replace it —
	// bumping from the oracle's estimate rather than from nil.
	adopted := chain.TxHash{0xaa}
	id, err := h.store.Adopt(ctx, txintent.Params{
		Kind: "Adopted", KeyParams: []byte{1}, To: chain.Address{0x01}, GasLimit: 21_000,
	}, adopted, 5)
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)
	for len(h.sentTxHashes()) < 1 {
		select {
		case <-deadline:
			t.Fatal("expected a replacement broadcast")
		case <-time.After(50 * time.Millisecond):
		}
	}
	replacement := h.sentTxHashes()[0]
	h.receipts.Set(replacement, &receipts.Receipt{TxHash: replacement, Status: 1, Confirmed: true})

	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	final, err := h.store.Wait(waitCtx, id)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != txintent.StatusConfirmed {
		t.Fatalf("status = %s (%v)", final.Status, final.FailedReason)
	}
	if len(final.Attempts) != 2 || final.Attempts[1].Nonce != 5 || final.Attempts[0].ReplacedAt == nil {
		t.Fatalf("expected the adopted attempt replaced at nonce 5, got %+v", final.Attempts)
	}
	bump := int64(100 + config.Default().TxIntent.ReplacementGasBump)
	want := new(big.Int).Mul(big.NewInt(3_000_000_000), big.NewInt(bump)) // fake oracle FeeCap
	want.Quo(want, big.NewInt(100))
	if final.Attempts[1].GasFeeCap.Cmp(want) != 0 {
		t.Fatalf("replacement fee cap = %s, want oracle fee cap bumped = %s", final.Attempts[1].GasFeeCap, want)
	}
	if n := len(h.sentTxHashes()); n != 1 {
		t.Fatalf("sends = %d, want exactly the replacement", n)
	}
}
