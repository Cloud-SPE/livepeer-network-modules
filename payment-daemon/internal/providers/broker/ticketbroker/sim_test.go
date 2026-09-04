package ticketbroker

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"path/filepath"
	"sync"
	"testing"
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"

	cchain "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	chaincfg "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/config"
	cerrors "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/errors"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/gasoracle/ttl"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/receipts/reorg"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/store"
	ccbolt "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/store/bolt"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"
	chaintesting "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing/simchain"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers"
)

const simRedeemGas = 200_000

// simHarness runs the real Broker over the real txintent processor
// against a simulated chain holding the stub TicketBroker: real
// signatures, real nonces, real receipts, real reverts.
type simHarness struct {
	sim      *simchain.Chain
	rpc      *simchain.Wrapper
	acct     *simchain.Account
	contract cchain.Address
	store    store.Store
	mgr      *txintent.Manager
	broker   *Broker
}

// newSimChain deploys the stub once per test.
func newSimChain(t *testing.T) (*simchain.Chain, cchain.Address) {
	t.Helper()
	sim := simchain.New(t)
	addr, _, err := sim.Deploy(context.Background(), sim.Accounts[0], stubInitCode(), "")
	if err != nil {
		t.Fatalf("deploy stub: %v", err)
	}
	return sim, addr
}

// attach builds a manager + broker over st against the chain.
func attach(t *testing.T, sim *simchain.Chain, contract cchain.Address, st store.Store) *simHarness {
	t.Helper()
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
	policy := chaincfg.Default().TxIntent
	policy.SubmitTimeout = 10 * time.Second
	policy.MaxReplacements = 1
	proc, err := txintent.NewDefaultProcessor(txintent.ProcessorConfig{
		Policy:             policy,
		ChainID:            simchain.DevChainID,
		ReorgConfirmations: 2,
		GasLimit:           simRedeemGas,
		RPC:                rpc,
		Keystore:           acct.Keystore,
		Gas:                gas,
		Receipts:           rcpts,
		Clock:              clock.System(),
		Logger:             chaintesting.NewFakeLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := txintent.New(policy, st, clock.System(), chaintesting.NewFakeLogger(), nil, proc)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(Config{Address: contract, Claimant: acct.Address, RedeemGas: simRedeemGas}, rpc, mgr)
	if err != nil {
		t.Fatal(err)
	}
	return &simHarness{sim: sim, rpc: rpc, acct: acct, contract: contract, store: st, mgr: mgr, broker: b}
}

func newSimHarness(t *testing.T) *simHarness {
	sim, addr := newSimChain(t)
	return attach(t, sim, addr, chaintesting.NewFakeStore())
}

// redeem calls the broker while a goroutine mines blocks, so the
// receipt and confirmations arrive.
func (h *simHarness) redeem(ctx context.Context, tk *providers.Ticket) ([]byte, error) {
	sig := bytes.Repeat([]byte{0x42}, 65)
	done := make(chan struct{})
	mineCtx, stop := context.WithCancel(ctx)
	go func() {
		defer close(done)
		h.sim.MineUntil(mineCtx, 10_000, func() bool {
			time.Sleep(2 * time.Millisecond)
			return mineCtx.Err() != nil
		})
	}()
	got, err := h.broker.RedeemWinningTicket(ctx, tk, sig, big.NewInt(7))
	stop()
	<-done
	return got, err
}

func simTicket(nonce uint32) *providers.Ticket {
	return &providers.Ticket{
		Recipient:         ethcommon.HexToAddress("0x2222222222222222222222222222222222222222").Bytes(),
		Sender:            ethcommon.HexToAddress("0x3333333333333333333333333333333333333333").Bytes(),
		FaceValue:         big.NewInt(1_000_000),
		WinProb:           big.NewInt(5),
		SenderNonce:       nonce,
		RecipientRandHash: bytes.Repeat([]byte{0x11}, 32),
		CreationRound:     42,
		CreationRoundHash: bytes.Repeat([]byte{0x99}, 32),
	}
}

func TestSim_StubAnswersReads(t *testing.T) {
	h := newSimHarness(t)
	ctx := context.Background()
	used, err := h.broker.IsUsedTicket(ctx, TicketHash(simTicket(1)))
	if err != nil || used {
		t.Fatalf("fresh ticket used=%v err=%v", used, err)
	}
	if v, err := h.broker.TicketValidityPeriod(ctx); err != nil || v != 2 {
		t.Fatalf("ticketValidityPeriod = %d, %v", v, err)
	}
}

func TestSim_RedeemLandsOneTxAndMarksUsed(t *testing.T) {
	h := newSimHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tk := simTicket(1)

	got, err := h.redeem(ctx, tk)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if n := h.rpc.Calls("SendTransaction"); n != 1 {
		t.Fatalf("SendTransaction calls = %d, want 1", n)
	}
	receipt, err := h.rpc.TransactionReceipt(ctx, ethcommon.BytesToHash(got))
	if err != nil || receipt.Status != 1 {
		t.Fatalf("receipt for returned hash: %v %v", receipt, err)
	}
	used, err := h.broker.IsUsedTicket(ctx, TicketHash(tk))
	if err != nil || !used {
		t.Fatalf("after redeem: used=%v err=%v — the stub's packed hash disagrees with types.Ticket.Hash", used, err)
	}
	// A different ticket is untouched.
	if used, _ := h.broker.IsUsedTicket(ctx, TicketHash(simTicket(2))); used {
		t.Fatal("unrelated ticket marked used")
	}
}

func TestSim_AlreadyUsedPrecheckSendsNothing(t *testing.T) {
	h := newSimHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tk := simTicket(1)
	if _, err := h.redeem(ctx, tk); err != nil {
		t.Fatal(err)
	}
	before := h.rpc.Calls("SendTransaction")

	// The same ticket again, and again from a broker with an empty
	// intent store (a daemon upgraded after the old loop landed it).
	if _, err := h.redeem(ctx, tk); !errors.Is(err, providers.ErrTicketAlreadyUsed) {
		t.Fatalf("second redeem: %v", err)
	}
	fresh := attach(t, h.sim, h.contract, chaintesting.NewFakeStore())
	if _, err := fresh.redeem(ctx, tk); !errors.Is(err, providers.ErrTicketAlreadyUsed) {
		t.Fatalf("fresh-store redeem: %v", err)
	}
	if h.rpc.Calls("SendTransaction") != before || fresh.rpc.Calls("SendTransaction") != 0 {
		t.Fatalf("a used ticket produced a transaction")
	}
}

func TestSim_RevertIsErrTxFailedAndNotRetried(t *testing.T) {
	h := newSimHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tk := simTicket(1)
	tk.FaceValue = big.NewInt(0) // the stub's forced-revert switch

	_, err := h.redeem(ctx, tk)
	if !errors.Is(err, ErrTxFailed) {
		t.Fatalf("err = %v, want ErrTxFailed", err)
	}
	if cerrors.Classify(err).Class != cerrors.ClassReverted {
		t.Fatalf("class = %s", cerrors.Classify(err).Class)
	}
	if n := h.rpc.Calls("SendTransaction"); n != 1 {
		t.Fatalf("sends = %d, want 1", n)
	}
	if used, _ := h.broker.IsUsedTicket(ctx, TicketHash(tk)); used {
		t.Fatal("reverted redemption marked the ticket used")
	}
	// The next settlement tick asks again: the failed intent is final
	// for a revert, so no second transaction.
	if _, err := h.redeem(ctx, tk); !errors.Is(err, ErrTxFailed) {
		t.Fatalf("second attempt: %v", err)
	}
	if n := h.rpc.Calls("SendTransaction"); n != 1 {
		t.Fatalf("sends after retry = %d, want 1", n)
	}
}

func TestSim_DuplicateSubmitsShareOneTx(t *testing.T) {
	h := newSimHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tk := simTicket(1)

	var wg sync.WaitGroup
	results := make([][]byte, 3)
	errs := make([]error, 3)
	sig := bytes.Repeat([]byte{0x42}, 65)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = h.broker.RedeemWinningTicket(ctx, tk, sig, big.NewInt(7))
		}(i)
	}
	mineCtx, stop := context.WithCancel(ctx)
	go h.sim.MineUntil(mineCtx, 10_000, func() bool { time.Sleep(2 * time.Millisecond); return mineCtx.Err() != nil })
	wg.Wait()
	stop()

	for i := range results {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if !bytes.Equal(results[i], results[0]) {
			t.Fatalf("call %d returned a different tx hash", i)
		}
	}
	if n := h.rpc.Calls("SendTransaction"); n != 1 {
		t.Fatalf("SendTransaction calls = %d, want 1 for three concurrent redeems of one ticket", n)
	}
}

func TestSim_TransientSendFailureIsRetried(t *testing.T) {
	h := newSimHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h.rpc.Only("SendTransaction").FailFirst(1, context.DeadlineExceeded)

	got, err := h.redeem(ctx, simTicket(1))
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if n := h.rpc.Calls("SendTransaction"); n != 2 {
		t.Fatalf("SendTransaction calls = %d, want 2 (one refused, one landed)", n)
	}
	if receipt, err := h.rpc.TransactionReceipt(ctx, ethcommon.BytesToHash(got)); err != nil || receipt.Status != 1 {
		t.Fatalf("receipt: %v %v", receipt, err)
	}
}

func TestSim_RestartResumesInFlightRedemptionWithoutResending(t *testing.T) {
	sim, contract := newSimChain(t)
	path := filepath.Join(t.TempDir(), "txintents.db")
	st1, err := ccbolt.Open(path, ccbolt.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h1 := attach(t, sim, contract, st1)
	tk := simTicket(1)
	sig := bytes.Repeat([]byte{0x42}, 65)

	// First life: submit, observe the broadcast, and go down before any
	// block is mined. The settlement tick's context is cancelled the
	// way a shutdown cancels it.
	ctx1, cancel1 := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := h1.broker.RedeemWinningTicket(ctx1, tk, sig, big.NewInt(7))
		errCh <- err
	}()
	// Down only once the store says submitted: a daemon killed between
	// sign and broadcast is the StatusSigned path, which legitimately
	// re-broadcasts the same signed bytes on resume.
	id := txintent.ComputeID(IntentKind, TicketHash(tk))
	deadline := time.Now().Add(10 * time.Second)
	for {
		cur, err := h1.mgr.Status(context.Background(), id)
		if err == nil && cur.Status == txintent.StatusSubmitted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("first life never reached submitted: %v %v", cur.Status, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if h1.rpc.Calls("SendTransaction") != 1 {
		t.Fatalf("first life sends = %d", h1.rpc.Calls("SendTransaction"))
	}
	cancel1()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted redeem: %v", err)
	}
	// Refuse any further sends from the first life's processor so a
	// leak would fail the test rather than land silently.
	h1.rpc.Only("SendTransaction").FailAlways(errors.New("first life is dead"))
	if err := st1.Close(); err != nil {
		t.Fatal(err)
	}

	// Second life: reopen the store, Resume, and let the chain move.
	st2, err := ccbolt.Open(path, ccbolt.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	h2 := attach(t, sim, contract, st2)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h2.mgr.Resume(ctx); err != nil {
		t.Fatalf("resume: %v", err)
	}
	got, err := h2.redeem(ctx, tk)
	if err != nil {
		t.Fatalf("redeem after restart: %v", err)
	}
	if n := h2.rpc.Calls("SendTransaction"); n != 0 {
		t.Fatalf("second life re-sent the redemption: %d sends", n)
	}
	if used, _ := h2.broker.IsUsedTicket(ctx, TicketHash(tk)); !used {
		t.Fatal("ticket not used after resumed confirmation")
	}
	// The hash returned is the first life's broadcast.
	intent, err := h2.mgr.Status(ctx, id)
	if err != nil || intent.Status != txintent.StatusConfirmed || len(intent.Attempts) != 1 {
		t.Fatalf("intent = %+v, %v", intent, err)
	}
	if !bytes.Equal(got, intent.Attempts[0].SignedTxHash.Bytes()) {
		t.Fatalf("returned hash %x != adopted attempt %s", got, intent.Attempts[0].SignedTxHash.Hex())
	}
}
