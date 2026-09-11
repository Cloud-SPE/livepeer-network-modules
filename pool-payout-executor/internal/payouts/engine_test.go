package payouts_test

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	ccconfig "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/config"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/store/bolt"
	chaintest "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing/simchain"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/payouts"
)

// revertingContract's runtime is PUSH1 0 PUSH1 0 REVERT; the init code
// copies those five bytes out and returns them.
const revertingContract = "600580600b6000396000f360006000fd"

type fixture struct {
	t     *testing.T
	sim   *simchain.Chain
	rpc   *simchain.Wrapper
	acct  *simchain.Account
	store store.Store
	cfg   config.Executor
	opts  payouts.Options
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	sim := simchain.New(t)
	return &fixture{
		t:     t,
		sim:   sim,
		rpc:   simchain.Wrap(sim.RPC()),
		acct:  sim.Accounts[0],
		store: chaintest.NewFakeStore(),
		cfg:   config.Executor{ConfirmationBlocks: 2},
		opts:  payouts.Options{ReceiptPoll: 2 * time.Millisecond, ConfirmWait: 3 * time.Second},
	}
}

func (f *fixture) open() *payouts.Engine {
	f.t.Helper()
	opts := f.opts
	opts.Store = f.store
	e, err := payouts.OpenWith(context.Background(), f.cfg, f.rpc, f.acct.Keystore, simchain.DevChainID, opts)
	if err != nil {
		f.t.Fatalf("OpenWith() error = %v", err)
	}
	f.t.Cleanup(func() { _ = e.Close() })
	return e
}

// mine commits a block every 2ms until the test ends.
func (f *fixture) mine() {
	ctx, cancel := context.WithCancel(context.Background())
	f.t.Cleanup(cancel)
	go f.sim.MineUntil(ctx, 1_000_000, func() bool {
		time.Sleep(2 * time.Millisecond)
		return ctx.Err() != nil
	})
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestDispatchConfirmsOnRealChain(t *testing.T) {
	f := newFixture(t)
	e := f.open()
	ctx := ctxT(t)
	to := f.sim.Accounts[1].Address
	before, _ := f.rpc.BalanceAt(ctx, to, nil)

	sent, err := e.Dispatch(ctx, "payout-1", to, big.NewInt(1_000))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if sent.Reused || sent.TxHash == "" {
		t.Fatalf("Dispatch() = %+v", sent)
	}
	f.mine()
	out, err := e.Outcome(ctx, "payout-1")
	if err != nil {
		t.Fatalf("Outcome() error = %v", err)
	}
	if out.State != payouts.StatePaid || out.TxHash != sent.TxHash || out.Nonce != sent.Nonce {
		t.Fatalf("Outcome() = %+v, want paid %s", out, sent.TxHash)
	}
	if n := f.rpc.Calls("SendTransaction"); n != 1 {
		t.Fatalf("SendTransaction calls = %d, want 1", n)
	}
	after, _ := f.rpc.BalanceAt(ctx, to, nil)
	if new(big.Int).Sub(after, before).Int64() != 1_000 {
		t.Fatalf("recipient gained %s, want 1000", new(big.Int).Sub(after, before))
	}
	if e.FromAddress() != f.acct.Address {
		t.Fatalf("FromAddress() = %s", e.FromAddress())
	}
	if bal, err := e.BalanceAt(ctx); err != nil || bal.Sign() <= 0 {
		t.Fatalf("BalanceAt() = %v, %v", bal, err)
	}
	if e.ConfirmationDepth() != 1 {
		t.Fatalf("ConfirmationDepth() = %d", e.ConfirmationDepth())
	}
}

func TestDispatchIsIdempotentAcrossCallsAndRestarts(t *testing.T) {
	f := newFixture(t)
	e := f.open()
	ctx := ctxT(t)
	to := f.sim.Accounts[1].Address

	first, err := e.Dispatch(ctx, "payout-dup", to, big.NewInt(500))
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.Dispatch(ctx, "payout-dup", to, big.NewInt(500))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused || second.TxHash != first.TxHash {
		t.Fatalf("second Dispatch = %+v, want reuse of %s", second, first.TxHash)
	}
	_ = e.Close()

	// A new process over the same store sees the same intent.
	e2 := f.open()
	third, err := e2.Dispatch(ctx, "payout-dup", to, big.NewInt(500))
	if err != nil {
		t.Fatal(err)
	}
	if !third.Reused || third.TxHash != first.TxHash {
		t.Fatalf("Dispatch after restart = %+v", third)
	}
	if n := f.rpc.Calls("SendTransaction"); n != 1 {
		t.Fatalf("SendTransaction calls = %d, want 1", n)
	}
	f.mine()
	out, err := e2.Outcome(ctx, "payout-dup")
	if err != nil || out.State != payouts.StatePaid {
		t.Fatalf("Outcome() = %+v, %v", out, err)
	}
}

func TestDispatchRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	e := f.open()
	ctx := ctxT(t)
	to := f.sim.Accounts[1].Address
	if _, err := e.Dispatch(ctx, " ", to, big.NewInt(1)); err == nil {
		t.Fatal("empty id accepted")
	}
	if _, err := e.Dispatch(ctx, "x", to, big.NewInt(0)); err == nil {
		t.Fatal("zero amount accepted")
	}
	// More than the wallet holds: the node refuses to estimate, so the
	// payout never becomes an intent.
	tooMuch := new(big.Int).Mul(simchain.DefaultBalance, big.NewInt(2))
	if _, err := e.Dispatch(ctx, "x", to, tooMuch); err == nil || !strings.Contains(err.Error(), "estimate gas") {
		t.Fatalf("overspend error = %v", err)
	}
	if _, err := e.Outcome(ctx, "x"); !errors.Is(err, payouts.ErrNotTracked) {
		t.Fatalf("Outcome(untracked) error = %v", err)
	}
	if n := f.rpc.Calls("SendTransaction"); n != 0 {
		t.Fatalf("SendTransaction calls = %d, want 0", n)
	}
}

func TestRevertedTransactionIsReportedFailed(t *testing.T) {
	f := newFixture(t)
	e := f.open()
	ctx := ctxT(t)

	code, _ := hex.DecodeString(revertingContract)
	contract, _, err := f.sim.Deploy(ctx, f.acct, code, "")
	if err != nil {
		t.Fatalf("Deploy() error = %v", err)
	}
	// The previous implementation would have sent this with its own gas
	// sizing; send it by hand so no estimate stands in the way.
	tx, err := f.sim.NewDynamicFeeTx(ctx, f.acct, &contract, big.NewInt(1), 100_000, nil)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := f.sim.SignTx(f.acct, tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.sim.RPC().SendTransaction(ctx, signed); err != nil {
		t.Fatalf("send: %v", err)
	}
	adopted, err := e.Track(ctx, "payout-revert", signed.Hash().Hex(), "", contract, big.NewInt(1))
	if err != nil || !adopted {
		t.Fatalf("Track() = %v, %v; want adopted", adopted, err)
	}
	f.mine()
	out, err := e.Outcome(ctx, "payout-revert")
	if err != nil {
		t.Fatal(err)
	}
	if out.State != payouts.StateFailed || !strings.Contains(out.Reason, "reverted") {
		t.Fatalf("Outcome() = %+v, want failed/reverted", out)
	}
	if n := f.rpc.Calls("SendTransaction"); n != 0 {
		t.Fatalf("processor sent %d txs for an adopted intent", n)
	}
	// Peek agrees, straight from the chain.
	peek, err := e.Peek(ctx, signed.Hash().Hex())
	if err != nil || peek.State != payouts.StateFailed {
		t.Fatalf("Peek() = %+v, %v", peek, err)
	}
}

func TestStalledTransactionIsReplacedThenConfirmed(t *testing.T) {
	f := newFixture(t)
	policy := ccconfig.Default().TxIntent
	policy.SubmitTimeout = 200 * time.Millisecond
	policy.MaxReplacements = 2
	f.opts.Policy = &policy
	e := f.open()
	ctx := ctxT(t)
	to := f.sim.Accounts[1].Address

	first, err := e.Dispatch(ctx, "payout-stall", to, big.NewInt(700))
	if err != nil {
		t.Fatal(err)
	}
	// Nobody mines, so the first attempt stalls past SubmitTimeout and
	// the processor re-signs at the same nonce with bumped gas.
	deadline := time.Now().Add(10 * time.Second)
	for f.rpc.Calls("SendTransaction") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("no replacement broadcast; sends = %d", f.rpc.Calls("SendTransaction"))
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.mine()
	out, err := e.Outcome(ctx, "payout-stall")
	if err != nil {
		t.Fatal(err)
	}
	if out.State != payouts.StatePaid {
		t.Fatalf("Outcome() = %+v, want paid", out)
	}
	if out.TxHash == first.TxHash {
		t.Fatalf("confirmed hash %s is the stalled attempt; replacement did not take", out.TxHash)
	}
	if out.Nonce != first.Nonce {
		t.Fatalf("replacement nonce %d != original %d", out.Nonce, first.Nonce)
	}
	if n := f.rpc.Calls("SendTransaction"); n != 2 {
		t.Fatalf("SendTransaction calls = %d, want 2", n)
	}
}

func TestRestartResumesInFlightIntentWithoutResending(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(t.TempDir(), "intents.db")
	st, err := bolt.Open(path, bolt.Default())
	if err != nil {
		t.Fatal(err)
	}
	f.store = st
	e := f.open()
	ctx := ctxT(t)
	to := f.sim.Accounts[1].Address

	sent, err := e.Dispatch(ctx, "payout-resume", to, big.NewInt(900))
	if err != nil {
		t.Fatal(err)
	}
	// Process dies before the tx is mined.
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := bolt.Open(path, bolt.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	f.store = st2
	e2 := f.open() // Resume runs inside OpenWith
	f.mine()
	out, err := e2.Outcome(ctx, "payout-resume")
	if err != nil {
		t.Fatal(err)
	}
	if out.State != payouts.StatePaid || out.TxHash != sent.TxHash {
		t.Fatalf("Outcome() after restart = %+v, want paid %s", out, sent.TxHash)
	}
	if n := f.rpc.Calls("SendTransaction"); n != 1 {
		t.Fatalf("SendTransaction calls across restart = %d, want 1", n)
	}
}

func TestTrackAdoptsControllerRecordedTransaction(t *testing.T) {
	f := newFixture(t)
	e := f.open()
	ctx := ctxT(t)
	to := f.sim.Accounts[1].Address

	// The previous implementation broadcast this and wrote nonce-N to the
	// controller; this executor has no record of it.
	tx, err := f.sim.SendValue(ctx, f.acct, to, big.NewInt(1_100))
	if err != nil {
		t.Fatal(err)
	}
	adopted, err := e.Track(ctx, "payout-adopt", tx.Hash().Hex(), "nonce-"+bigU(tx.Nonce()), to, big.NewInt(1_100))
	if err != nil || !adopted {
		t.Fatalf("Track() = %v, %v; want adopted", adopted, err)
	}
	// Second Track is a no-op.
	adopted, err = e.Track(ctx, "payout-adopt", tx.Hash().Hex(), "", to, big.NewInt(1_100))
	if err != nil || adopted {
		t.Fatalf("second Track() = %v, %v; want false", adopted, err)
	}
	f.mine()
	out, err := e.Outcome(ctx, "payout-adopt")
	if err != nil {
		t.Fatal(err)
	}
	if out.State != payouts.StatePaid || out.TxHash != tx.Hash().Hex() || out.Nonce != tx.Nonce() {
		t.Fatalf("Outcome() = %+v", out)
	}
	if n := f.rpc.Calls("SendTransaction"); n != 0 {
		t.Fatalf("adopted intent was re-sent %d times", n)
	}
	// Dispatch after Track reuses, never pays twice.
	again, err := e.Dispatch(ctx, "payout-adopt", to, big.NewInt(1_100))
	if err != nil || !again.Reused || again.TxHash != tx.Hash().Hex() {
		t.Fatalf("Dispatch after adopt = %+v, %v", again, err)
	}
}

func TestTrackReadsNonceFromChainWhenControllerLacksIt(t *testing.T) {
	f := newFixture(t)
	e := f.open()
	ctx := ctxT(t)
	to := f.sim.Accounts[1].Address
	tx, err := f.sim.SendValue(ctx, f.acct, to, big.NewInt(300))
	if err != nil {
		t.Fatal(err)
	}
	adopted, err := e.Track(ctx, "payout-nonce-from-chain", tx.Hash().Hex(), "not-a-nonce", to, big.NewInt(300))
	if err != nil || !adopted {
		t.Fatalf("Track() = %v, %v", adopted, err)
	}
	f.mine()
	out, err := e.Outcome(ctx, "payout-nonce-from-chain")
	if err != nil || out.State != payouts.StatePaid || out.Nonce != tx.Nonce() {
		t.Fatalf("Outcome() = %+v, %v", out, err)
	}
}

func TestTrackLeavesUnknownTransactionAlone(t *testing.T) {
	f := newFixture(t)
	e := f.open()
	ctx := ctxT(t)
	to := f.sim.Accounts[1].Address
	unknown := "0x" + strings.Repeat("ab", 32)
	adopted, err := e.Track(ctx, "payout-unknown", unknown, "", to, big.NewInt(1))
	if !errors.Is(err, payouts.ErrUnknownTx) || adopted {
		t.Fatalf("Track(unknown) = %v, %v; want ErrUnknownTx", adopted, err)
	}
	if _, err := e.Outcome(ctx, "payout-unknown"); !errors.Is(err, payouts.ErrNotTracked) {
		t.Fatalf("Outcome() error = %v", err)
	}
	if _, err := e.Track(ctx, "payout-nohash", "", "", to, big.NewInt(1)); err == nil {
		t.Fatal("Track without a hash accepted")
	}
	// With a recorded nonce the unknown tx is adopted anyway; the
	// processor's replacement path takes over from there.
	adopted, err = e.Track(ctx, "payout-unknown-with-nonce", unknown, "nonce-5", to, big.NewInt(1))
	if err != nil || !adopted {
		t.Fatalf("Track(unknown, nonce) = %v, %v", adopted, err)
	}
	peek, err := e.Peek(ctx, unknown)
	if err != nil || peek.State != payouts.StatePending {
		t.Fatalf("Peek(unknown) = %+v, %v", peek, err)
	}
}

func TestOutcomeReportsPendingWhileUnmined(t *testing.T) {
	f := newFixture(t)
	f.opts.ConfirmWait = 50 * time.Millisecond
	e := f.open()
	ctx := ctxT(t)
	to := f.sim.Accounts[1].Address
	sent, err := e.Dispatch(ctx, "payout-pending", to, big.NewInt(1))
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.Outcome(ctx, "payout-pending")
	if err != nil || out.State != payouts.StatePending || out.TxHash != sent.TxHash {
		t.Fatalf("Outcome() = %+v, %v; want pending", out, err)
	}
	peek, err := e.Peek(ctx, sent.TxHash)
	if err != nil || peek.State != payouts.StatePending {
		t.Fatalf("Peek() = %+v, %v; want pending", peek, err)
	}
	// One block mines it; the depth of 1 is not reached until the next.
	f.sim.Commit()
	peek, err = e.Peek(ctx, sent.TxHash)
	if err != nil || peek.State != payouts.StatePending {
		t.Fatalf("Peek() after inclusion = %+v, %v; want pending", peek, err)
	}
	f.sim.Commit()
	peek, err = e.Peek(ctx, sent.TxHash)
	if err != nil || peek.State != payouts.StatePaid {
		t.Fatalf("Peek() after depth = %+v, %v; want paid", peek, err)
	}
	f.mine()
	out, err = e.Outcome(ctx, "payout-pending")
	if err != nil || out.State != payouts.StatePaid {
		t.Fatalf("Outcome() = %+v, %v; want paid", out, err)
	}
	// A cancelled caller context surfaces as its own error.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := e.Dispatch(cctx, "payout-cancelled", to, big.NewInt(1)); err == nil {
		t.Fatal("Dispatch with cancelled ctx succeeded")
	}
}

func TestOpenWithValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := payouts.OpenWith(ctx, f.cfg, nil, f.acct.Keystore, simchain.DevChainID, f.opts); err == nil {
		t.Fatal("nil rpc accepted")
	}
	if _, err := payouts.OpenWith(ctx, f.cfg, f.rpc, nil, simchain.DevChainID, f.opts); err == nil {
		t.Fatal("nil keystore accepted")
	}
	if _, err := payouts.OpenWith(ctx, f.cfg, f.rpc, f.acct.Keystore, 0, f.opts); err == nil {
		t.Fatal("zero chain id accepted")
	}
	// Config-derived defaults: store path, confirm wait, replacement policy.
	cfg := config.Executor{StatePath: filepath.Join(t.TempDir(), "state.db"), ConfirmWaitMS: 10, ReplaceAfterSeconds: 1, MaxReplacements: 1, ConfirmationBlocks: 4}
	e, err := payouts.OpenWith(ctx, cfg, f.rpc, f.acct.Keystore, simchain.DevChainID, payouts.Options{})
	if err != nil {
		t.Fatalf("OpenWith(bolt default) error = %v", err)
	}
	if e.ConfirmationDepth() != 3 {
		t.Fatalf("ConfirmationDepth() = %d, want 3", e.ConfirmationDepth())
	}
	if err := e.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := payouts.OpenWith(ctx, config.Executor{IntentStorePath: "/proc/nope/intents.db"}, f.rpc, f.acct.Keystore, simchain.DevChainID, payouts.Options{}); err == nil {
		t.Fatal("unwritable store path accepted")
	}
}

func TestOpenBuildsTransportFromConfig(t *testing.T) {
	t.Setenv("POOL_PAYOUT_EXECUTOR_TEST_KEY", "4c0883a69102937d6231471b5dbb6204fe512961708279ee7c36f6ddf6e702a8")
	cfg := config.Executor{PrivateKeyRef: "env://POOL_PAYOUT_EXECUTOR_TEST_KEY", RPCURLs: []string{"http://127.0.0.1:1"}, IntentStorePath: filepath.Join(t.TempDir(), "i.db")}
	policy := ccconfig.Default().RPC
	policy.MaxRetries = 0
	policy.InitialBackoff = time.Millisecond
	policy.CallTimeout = 200 * time.Millisecond
	if _, err := payouts.Open(context.Background(), cfg, payouts.Options{RPCPolicy: &policy}); err == nil {
		t.Fatal("Open against a closed port succeeded")
	}
	if _, err := payouts.Open(context.Background(), config.Executor{}, payouts.Options{}); err == nil {
		t.Fatal("Open without rpc_urls succeeded")
	}
}

func bigU(n uint64) string { return new(big.Int).SetUint64(n).String() }

var _ = chain.Address{}
var _ = ethcommon.Address{}
