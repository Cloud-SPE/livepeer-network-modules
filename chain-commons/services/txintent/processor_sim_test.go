package txintent_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/config"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/gasoracle/ttl"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/receipts/reorg"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"
	chaintest "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing/simchain"
	"github.com/ethereum/go-ethereum/params"
)

// simHarness drives the real DefaultProcessor against a simulated chain:
// real signatures, real nonces, real receipts. Every RPC call goes
// through a Wrapper so tests can count sends.
type simHarness struct {
	sim   *simchain.Chain
	rpc   *simchain.Wrapper
	acct  *simchain.Account
	mgr   *txintent.Manager
	confs uint64
}

func newSimHarness(t *testing.T) *simHarness {
	t.Helper()
	sim := simchain.New(t)
	rpc := simchain.Wrap(sim.RPC())
	acct := sim.Accounts[0]

	gas, err := ttl.New(ttl.Options{RPC: rpc, TTL: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	rcpts, err := reorg.New(reorg.Options{RPC: rpc, Poll: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	policy := config.Default().TxIntent
	policy.SubmitTimeout = 10 * time.Second
	policy.MaxReplacements = 1

	const confs = 2
	proc, err := txintent.NewDefaultProcessor(txintent.ProcessorConfig{
		Policy:             policy,
		ChainID:            simchain.DevChainID,
		ReorgConfirmations: confs,
		GasLimit:           params.TxGas,
		RPC:                rpc,
		Keystore:           acct.Keystore,
		Gas:                gas,
		Receipts:           rcpts,
		Clock:              clock.System(),
		Logger:             chaintest.NewFakeLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := txintent.New(policy, chaintest.NewFakeStore(), clock.System(), chaintest.NewFakeLogger(), nil, proc)
	if err != nil {
		t.Fatal(err)
	}
	return &simHarness{sim: sim, rpc: rpc, acct: acct, mgr: mgr, confs: confs}
}

// mineWhileWaiting commits a block every few milliseconds until the
// intent is terminal, so receipts and confirmations arrive.
func (h *simHarness) mineWhileWaiting(ctx context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.sim.MineUntil(ctx, 10_000, func() bool {
			cur, err := h.mgr.Status(ctx, id)
			if err != nil {
				return true
			}
			if cur.Status.IsTerminal() {
				return true
			}
			time.Sleep(2 * time.Millisecond)
			return false
		})
	}()
	got, err := h.mgr.Wait(ctx, id)
	<-done
	return got, err
}

func transferParams(to chain.Address, key byte) txintent.Params {
	return txintent.Params{
		Kind:      "Transfer",
		KeyParams: []byte{key},
		To:        to,
		Value:     big.NewInt(1_000),
		GasLimit:  params.TxGas,
	}
}

func TestProcessorSim_SubmitConfirmsOnRealChain(t *testing.T) {
	h := newSimHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	to := h.sim.Accounts[1].Address

	id, err := h.mgr.Submit(ctx, transferParams(to, 1))
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.mineWhileWaiting(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != txintent.StatusConfirmed {
		t.Fatalf("status = %s (%v)", got.Status, got.FailedReason)
	}
	if h.rpc.Calls("SendTransaction") != 1 {
		t.Fatalf("SendTransaction calls = %d, want 1", h.rpc.Calls("SendTransaction"))
	}
	cur := got.CurrentAttempt()
	receipt, err := h.rpc.TransactionReceipt(ctx, cur.SignedTxHash)
	if err != nil || receipt.Status != 1 {
		t.Fatalf("receipt for %s: %v %v", cur.SignedTxHash.Hex(), receipt, err)
	}
	bal, _ := h.rpc.BalanceAt(ctx, to, nil)
	want := new(big.Int).Add(simchain.DefaultBalance, big.NewInt(1_000))
	if bal.Cmp(want) != 0 {
		t.Fatalf("recipient balance = %s, want %s", bal, want)
	}
}

func TestProcessorSim_AdoptTracksForeignTxWithoutResending(t *testing.T) {
	h := newSimHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	to := h.sim.Accounts[1].Address

	// The "previous implementation" sends a transaction directly, not
	// through the processor's RPC wrapper.
	tx, err := h.sim.SendValue(ctx, h.acct, to, big.NewInt(1_000))
	if err != nil {
		t.Fatal(err)
	}

	id, err := h.mgr.Adopt(ctx, transferParams(to, 2), tx.Hash(), tx.Nonce(),
		txintent.WithGasCaps(tx.GasFeeCap(), tx.GasTipCap()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.mineWhileWaiting(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != txintent.StatusConfirmed {
		t.Fatalf("status = %s (%v)", got.Status, got.FailedReason)
	}
	if n := h.rpc.Calls("SendTransaction"); n != 0 {
		t.Fatalf("processor re-sent an adopted tx: SendTransaction calls = %d", n)
	}
	if len(got.Attempts) != 1 || got.Attempts[0].SignedTxHash != tx.Hash() {
		t.Fatalf("attempts = %+v", got.Attempts)
	}
	// The wallet's next nonce is exactly one past the adopted tx: nothing
	// else was signed for it.
	nonce, _ := h.rpc.PendingNonceAt(ctx, h.acct.Address)
	if nonce != tx.Nonce()+1 {
		t.Fatalf("pending nonce = %d, want %d", nonce, tx.Nonce()+1)
	}
}

func TestProcessorSim_SubmitAfterAdoptDoesNotDoubleSpend(t *testing.T) {
	h := newSimHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	to := h.sim.Accounts[1].Address

	tx, err := h.sim.SendValue(ctx, h.acct, to, big.NewInt(1_000))
	if err != nil {
		t.Fatal(err)
	}
	p := transferParams(to, 3)
	id, err := h.mgr.Adopt(ctx, p, tx.Hash(), tx.Nonce())
	if err != nil {
		t.Fatal(err)
	}
	// The daemon's normal path submits the same logical operation again,
	// as it would after a restart: idempotency returns the adopted intent.
	id2, err := h.mgr.Submit(ctx, p)
	if err != nil || id2 != id {
		t.Fatalf("Submit after Adopt: %v %v", id2, err)
	}
	got, err := h.mineWhileWaiting(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != txintent.StatusConfirmed || h.rpc.Calls("SendTransaction") != 0 {
		t.Fatalf("status = %s, sends = %d", got.Status, h.rpc.Calls("SendTransaction"))
	}
	bal, _ := h.rpc.BalanceAt(ctx, to, nil)
	want := new(big.Int).Add(simchain.DefaultBalance, big.NewInt(1_000))
	if bal.Cmp(want) != 0 {
		t.Fatalf("recipient paid %s over genesis, want exactly 1000", new(big.Int).Sub(bal, simchain.DefaultBalance))
	}
}

func TestProcessorSim_FailoverAfterTransientSendError(t *testing.T) {
	h := newSimHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	to := h.sim.Accounts[1].Address

	// The endpoint refuses the first broadcast with a transient error; the
	// processor's retry loop must move on and land the tx on the second.
	h.rpc.Only("SendTransaction").FailFirst(1, context.DeadlineExceeded)

	id, err := h.mgr.Submit(ctx, transferParams(to, 4))
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.mineWhileWaiting(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != txintent.StatusConfirmed {
		t.Fatalf("status = %s (%v)", got.Status, got.FailedReason)
	}
	if n := h.rpc.Calls("SendTransaction"); n != 2 {
		t.Fatalf("SendTransaction calls = %d, want 2 (one refused, one landed)", n)
	}
}
