// Package payouts drives every payout transaction through chain-commons's
// durable transaction intents. One intent per controller payout intent,
// keyed by the controller's id, so a restart, a retry, or a second
// executor run over the same batch can never send the same payout twice.
// The intent processor owns the hot wallet's nonce, gas-bump replacement
// of stalled transactions, and reorg-aware confirmation; the executor
// only asks for outcomes and reports them to pool-controller.
package payouts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	ccconfig "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/config"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/gasoracle/ttl"
	cckeystore "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/keystore"
	ccmetrics "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/receipts/reorg"
	ccrpc "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/rpc"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/store/bolt"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/chainlog"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/config"
	ethclientx "github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/ethclient"
)

// Kind is the intent kind every payout is filed under.
const Kind = "payout"

// State is the executor-facing outcome of a payout intent. The names are
// the controller's status vocabulary so callers map one-to-one.
type State string

const (
	StatePending State = "pending"
	StatePaid    State = "paid"
	StateFailed  State = "failed"
)

// Outcome is what the executor reports to pool-controller for one payout.
type Outcome struct {
	State  State
	TxHash string
	Nonce  uint64
	// Reason is set for StateFailed: the chain-commons error code and
	// message (e.g. "tx.reverted: transaction 0x… reverted on-chain").
	Reason string
}

// Dispatched describes a payout after its first broadcast.
type Dispatched struct {
	TxHash string
	Nonce  uint64
	// Reused is true when the intent already existed (an earlier run
	// broadcast it and died before telling the controller). Nothing was
	// sent this time.
	Reused bool
}

var (
	// ErrUnknownTx is returned by Track when the controller records a tx
	// hash but no nonce, and no endpoint knows the transaction. The
	// executor leaves such an intent for the controller's stale-submitted
	// handling rather than guessing a nonce to adopt it under.
	ErrUnknownTx = errors.New("payouts: transaction unknown to every endpoint")
	// ErrNotTracked is returned by Outcome for a payout no intent exists for.
	ErrNotTracked = errors.New("payouts: no intent for this payout")
)

// Options tunes Open and OpenWith. Zero values mean slog.Default(), a
// no-op recorder, chain-commons's default RPC policy, a 5 s receipt poll,
// a BoltDB store at cfg.IntentStoreFile(), and cfg.ConfirmWaitMS.
type Options struct {
	Logger      *slog.Logger
	Metrics     ccmetrics.Recorder
	RPCPolicy   *ccconfig.RPCPolicy
	ReceiptPoll time.Duration
	Store       store.Store
	ConfirmWait time.Duration
	// Policy overrides the replacement policy derived from cfg. Tests use
	// it for sub-second stall timeouts.
	Policy *ccconfig.TxIntentPolicy
}

// Engine owns the intent manager, the processor, and their store for one
// process. Open it once and Close it when the process is done; the
// processor goroutines it spawns are cancelled on Close.
type Engine struct {
	rpc     ccrpc.RPC
	from    ethcommon.Address
	chainID chain.ChainID
	confs   uint64

	st        store.Store
	ownsStore bool
	closeFn   func()

	mgr         *txintent.Manager
	proc        *txintent.DefaultProcessor
	confirmWait time.Duration
	log         *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	running map[txintent.IntentID]bool
}

// Open builds the transport and keystore from cfg (via ethclient.New) and
// wires the engine over them.
func Open(ctx context.Context, cfg config.Executor, opts Options) (*Engine, error) {
	client, err := ethclientx.New(ctx, cfg, ethclientx.Options{Logger: opts.Logger, Metrics: opts.Metrics, Policy: opts.RPCPolicy})
	if err != nil {
		return nil, err
	}
	e, err := OpenWith(ctx, cfg, client.RPC(), client.Keystore(), client.ChainID(), opts)
	if err != nil {
		client.Close()
		return nil, err
	}
	e.closeFn = client.Close
	return e, nil
}

// OpenWith wires the engine over an already-open transport and keystore.
// Tests inject a simulated chain here. Non-terminal intents in the store
// are resumed before OpenWith returns.
func OpenWith(ctx context.Context, cfg config.Executor, rpc ccrpc.RPC, ks cckeystore.Keystore, chainID chain.ChainID, opts Options) (*Engine, error) {
	if rpc == nil {
		return nil, errors.New("payouts: rpc is required")
	}
	if ks == nil {
		return nil, errors.New("payouts: keystore is required")
	}
	if chainID == 0 {
		return nil, errors.New("payouts: chain id is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Metrics == nil {
		opts.Metrics = ccmetrics.NoOp()
	}
	if opts.ReceiptPoll == 0 {
		opts.ReceiptPoll = 5 * time.Second
	}
	confirmWait := opts.ConfirmWait
	if confirmWait == 0 {
		confirmWait = 2 * time.Second
		if cfg.ConfirmWaitMS > 0 {
			confirmWait = time.Duration(cfg.ConfirmWaitMS) * time.Millisecond
		}
	}

	st := opts.Store
	ownsStore := false
	if st == nil {
		var err error
		st, err = bolt.Open(cfg.IntentStoreFile(), bolt.Default())
		if err != nil {
			return nil, fmt.Errorf("payouts: open intent store: %w", err)
		}
		ownsStore = true
	}

	policy := ccconfig.Default().TxIntent
	if cfg.ReplaceAfterSeconds > 0 {
		policy.SubmitTimeout = time.Duration(cfg.ReplaceAfterSeconds) * time.Second
	}
	if cfg.MaxReplacements > 0 {
		policy.MaxReplacements = cfg.MaxReplacements
	}
	if opts.Policy != nil {
		policy = *opts.Policy
	}

	log := chainlog.New(opts.Logger.With("component", "payouts"))
	gas, err := ttl.New(ttl.Options{RPC: rpc, TTL: 5 * time.Second})
	if err != nil {
		closeIfOwned(st, ownsStore)
		return nil, fmt.Errorf("payouts: gas oracle: %w", err)
	}
	rcpts, err := reorg.New(reorg.Options{RPC: rpc, Poll: opts.ReceiptPoll})
	if err != nil {
		closeIfOwned(st, ownsStore)
		return nil, fmt.Errorf("payouts: receipts: %w", err)
	}
	confs := reorgConfirmations(cfg.ConfirmationBlocks)
	proc, err := txintent.NewDefaultProcessor(txintent.ProcessorConfig{
		Policy:             policy,
		ChainID:            chainID,
		ReorgConfirmations: confs,
		GasLimit:           params.TxGas,
		RPC:                rpc,
		Keystore:           ks,
		Gas:                gas,
		Receipts:           rcpts,
		Clock:              clock.System(),
		Logger:             log,
		Metrics:            opts.Metrics,
	})
	if err != nil {
		closeIfOwned(st, ownsStore)
		return nil, fmt.Errorf("payouts: processor: %w", err)
	}
	// The manager gets no processor of its own: the engine spawns and
	// cancels processing goroutines itself, so a one-shot command can
	// stop cleanly and a long-running loop keeps tracking in the
	// background.
	mgr, err := txintent.New(policy, st, clock.System(), log, opts.Metrics, nil)
	if err != nil {
		closeIfOwned(st, ownsStore)
		return nil, fmt.Errorf("payouts: intent manager: %w", err)
	}

	ectx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	e := &Engine{
		rpc:         rpc,
		from:        ks.Address(),
		chainID:     chainID,
		confs:       confs,
		st:          st,
		ownsStore:   ownsStore,
		mgr:         mgr,
		proc:        proc,
		confirmWait: confirmWait,
		log:         opts.Logger,
		ctx:         ectx,
		cancel:      cancel,
		running:     make(map[txintent.IntentID]bool),
	}
	if err := e.Resume(ctx); err != nil {
		_ = e.Close()
		return nil, err
	}
	return e, nil
}

// reorgConfirmations maps the executor's confirmation_blocks (N blocks
// including the one the tx is mined in; 0 or 1 = the receipt alone) to
// chain-commons's depth (blocks after the mined one). The minimum is one
// block after inclusion: the processor treats 0 as "use the default".
func reorgConfirmations(confirmationBlocks uint64) uint64 {
	if confirmationBlocks <= 2 {
		return 1
	}
	return confirmationBlocks - 1
}

func closeIfOwned(st store.Store, owned bool) {
	if owned {
		_ = st.Close()
	}
}

// Close cancels every processing goroutine, waits briefly for them, then
// closes the store and the transport.
func (e *Engine) Close() error {
	e.cancel()
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		e.log.Warn("payouts: processor goroutines did not stop within 5s; closing anyway")
	}
	var err error
	if e.ownsStore {
		err = e.st.Close()
	}
	if e.closeFn != nil {
		e.closeFn()
	}
	return err
}

// FromAddress is the hot wallet.
func (e *Engine) FromAddress() ethcommon.Address { return e.from }

// BalanceAt is the hot wallet's current balance.
func (e *Engine) BalanceAt(ctx context.Context) (*big.Int, error) {
	return e.rpc.BalanceAt(ctx, e.from, nil)
}

// ConfirmationDepth is the number of blocks after inclusion an intent
// needs before it is reported paid.
func (e *Engine) ConfirmationDepth() uint64 { return e.confs }

// Resume re-drives every non-terminal intent in the store.
func (e *Engine) Resume(ctx context.Context) error {
	intents, err := e.mgr.List(ctx, txintent.Filter{Statuses: []txintent.IntentStatus{
		txintent.StatusPending, txintent.StatusSigned, txintent.StatusSubmitted, txintent.StatusMined, txintent.StatusReplaced,
	}})
	if err != nil {
		return fmt.Errorf("payouts: list intents: %w", err)
	}
	for _, t := range intents {
		e.spawn(t.ID)
	}
	if len(intents) > 0 {
		e.log.Info("payouts: resumed in-flight intents", "count", len(intents))
	}
	return nil
}

// spawn runs the processor for id unless it is already running.
func (e *Engine) spawn(id txintent.IntentID) {
	e.mu.Lock()
	if e.running[id] {
		e.mu.Unlock()
		return
	}
	e.running[id] = true
	e.mu.Unlock()
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer func() {
			e.mu.Lock()
			delete(e.running, id)
			e.mu.Unlock()
		}()
		e.proc.Process(e.ctx, e.mgr, id)
	}()
}

func intentID(controllerIntentID string) txintent.IntentID {
	return txintent.ComputeID(Kind, []byte(controllerIntentID))
}

func params_(controllerIntentID string, to ethcommon.Address, amountWei *big.Int, gasLimit uint64) txintent.Params {
	return txintent.Params{
		Kind:      Kind,
		KeyParams: []byte(controllerIntentID),
		To:        to,
		Value:     amountWei,
		GasLimit:  gasLimit,
		Metadata:  map[string]string{"controller_intent_id": controllerIntentID},
	}
}

// Dispatch files an intent for the payout and returns once it has been
// broadcast (or reused, or failed before broadcast). The gas limit is the
// node's estimate plus 10%, as the previous implementation sized it.
func (e *Engine) Dispatch(ctx context.Context, controllerIntentID string, to ethcommon.Address, amountWei *big.Int) (Dispatched, error) {
	if strings.TrimSpace(controllerIntentID) == "" {
		return Dispatched{}, errors.New("payouts: controller intent id is required")
	}
	if amountWei == nil || amountWei.Sign() <= 0 {
		return Dispatched{}, errors.New("payouts: amount must be > 0")
	}
	id := intentID(controllerIntentID)
	if existing, err := e.mgr.Status(ctx, id); err == nil {
		return e.awaitBroadcast(ctx, id, existing, true)
	} else if !errors.Is(err, store.ErrNotFound) {
		return Dispatched{}, fmt.Errorf("payouts: read intent: %w", err)
	}

	gasLimit, err := e.rpc.EstimateGas(ctx, ethereum.CallMsg{From: e.from, To: &to, Value: amountWei})
	if err != nil {
		return Dispatched{}, fmt.Errorf("estimate gas: %w", err)
	}
	if gasLimit == 0 {
		return Dispatched{}, errors.New("estimate gas returned 0")
	}
	gasLimit += gasLimit / 10

	if _, err := e.mgr.Submit(ctx, params_(controllerIntentID, to, amountWei, gasLimit)); err != nil {
		return Dispatched{}, fmt.Errorf("payouts: submit intent: %w", err)
	}
	cur, err := e.mgr.Status(ctx, id)
	if err != nil {
		return Dispatched{}, fmt.Errorf("payouts: read intent: %w", err)
	}
	return e.awaitBroadcast(ctx, id, cur, false)
}

// awaitBroadcast makes sure the processor is driving id and blocks until
// the intent has a broadcast attempt or is terminal.
func (e *Engine) awaitBroadcast(ctx context.Context, id txintent.IntentID, cur txintent.TxIntent, reused bool) (Dispatched, error) {
	for {
		if cur.Status.IsTerminal() {
			if cur.Status == txintent.StatusFailed {
				return Dispatched{}, fmt.Errorf("payouts: intent failed before broadcast: %s", failReason(cur))
			}
			return dispatched(cur, reused), nil
		}
		if a := cur.CurrentAttempt(); a != nil && cur.Status != txintent.StatusSigned {
			e.spawn(id)
			return dispatched(cur, reused), nil
		}
		e.spawn(id)
		select {
		case <-ctx.Done():
			return Dispatched{}, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
		var err error
		cur, err = e.mgr.Status(ctx, id)
		if err != nil {
			return Dispatched{}, fmt.Errorf("payouts: read intent: %w", err)
		}
	}
}

func dispatched(t txintent.TxIntent, reused bool) Dispatched {
	a := t.CurrentAttempt()
	if a == nil {
		return Dispatched{Reused: reused}
	}
	return Dispatched{TxHash: a.SignedTxHash.Hex(), Nonce: a.Nonce, Reused: reused}
}

// Track makes sure a payout the controller already records as submitted
// is tracked to confirmation. If an intent exists it is (re)driven;
// otherwise the transaction is adopted under the controller's id. The
// nonce comes from the controller's external_ref ("nonce-N", which this
// executor has always written) or, failing that, from the transaction
// itself; when neither is available Track returns ErrUnknownTx and
// leaves the intent alone. Returns true when an adoption happened.
func (e *Engine) Track(ctx context.Context, controllerIntentID, txHash, externalRef string, to ethcommon.Address, amountWei *big.Int) (bool, error) {
	id := intentID(controllerIntentID)
	if cur, err := e.mgr.Status(ctx, id); err == nil {
		if !cur.Status.IsTerminal() {
			e.spawn(id)
		}
		return false, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, fmt.Errorf("payouts: read intent: %w", err)
	}
	hash := ethcommon.HexToHash(strings.TrimSpace(txHash))
	if hash == (ethcommon.Hash{}) {
		return false, errors.New("payouts: tx hash is required to track a submitted payout")
	}

	var (
		nonce     uint64
		haveNonce bool
		gasLimit  = uint64(params.TxGas)
		opts      []txintent.AdoptOption
	)
	if n, ok := nonceFromExternalRef(externalRef); ok {
		nonce, haveNonce = n, true
	}
	tx, _, err := e.rpc.TransactionByHash(ctx, hash)
	switch {
	case err == nil && tx != nil:
		nonce, haveNonce = tx.Nonce(), true
		if tx.Gas() > 0 {
			gasLimit = tx.Gas()
		}
		opts = append(opts, txintent.WithGasCaps(tx.GasFeeCap(), tx.GasTipCap()))
	case err != nil && !errors.Is(err, ethereum.NotFound):
		return false, fmt.Errorf("payouts: look up %s: %w", hash.Hex(), err)
	}
	if !haveNonce {
		return false, ErrUnknownTx
	}
	if amountWei == nil {
		amountWei = new(big.Int)
	}
	if _, err := e.mgr.Adopt(ctx, params_(controllerIntentID, to, amountWei, gasLimit), hash, nonce, opts...); err != nil {
		return false, fmt.Errorf("payouts: adopt %s: %w", hash.Hex(), err)
	}
	e.log.Info("payouts: adopted in-flight payout", "controller_intent_id", controllerIntentID, "tx_hash", hash.Hex(), "nonce", nonce)
	e.spawn(id)
	return true, nil
}

func nonceFromExternalRef(ref string) (uint64, bool) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "nonce-") {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(ref, "nonce-"), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Outcome reports the payout's state, waiting up to the configured
// confirm wait for an in-flight one to settle. A payout with no intent
// returns ErrNotTracked.
func (e *Engine) Outcome(ctx context.Context, controllerIntentID string) (Outcome, error) {
	id := intentID(controllerIntentID)
	cur, err := e.mgr.Status(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Outcome{}, ErrNotTracked
		}
		return Outcome{}, fmt.Errorf("payouts: read intent: %w", err)
	}
	if cur.Status.IsTerminal() {
		return outcome(cur), nil
	}
	e.spawn(id)
	wctx, cancel := context.WithTimeout(ctx, e.confirmWait)
	defer cancel()
	settled, err := e.mgr.Wait(wctx, id)
	if err != nil {
		if ctx.Err() != nil {
			return Outcome{}, ctx.Err()
		}
		// Timed out: still in flight.
		cur, rerr := e.mgr.Status(ctx, id)
		if rerr != nil {
			return Outcome{}, fmt.Errorf("payouts: read intent: %w", rerr)
		}
		return outcome(cur), nil
	}
	return outcome(settled), nil
}

func outcome(t txintent.TxIntent) Outcome {
	o := Outcome{State: StatePending}
	if a := t.CurrentAttempt(); a != nil {
		o.TxHash = a.SignedTxHash.Hex()
		o.Nonce = a.Nonce
	}
	switch t.Status {
	case txintent.StatusConfirmed:
		o.State = StatePaid
	case txintent.StatusFailed:
		o.State = StateFailed
		o.Reason = failReason(t)
	}
	return o
}

func failReason(t txintent.TxIntent) string {
	if t.FailedReason == nil {
		return "failed"
	}
	return t.FailedReason.Error()
}

// Peek reads a transaction's outcome straight from the chain without
// touching the intent store. Dry-run previews use it so they stay
// read-only; the answer applies the same confirmation depth the
// processor uses.
func (e *Engine) Peek(ctx context.Context, txHash string) (Outcome, error) {
	hash := ethcommon.HexToHash(strings.TrimSpace(txHash))
	receipt, err := e.rpc.TransactionReceipt(ctx, hash)
	if err != nil {
		if errors.Is(err, ethereum.NotFound) {
			return Outcome{State: StatePending, TxHash: hash.Hex()}, nil
		}
		return Outcome{}, fmt.Errorf("transaction receipt: %w", err)
	}
	if receipt == nil {
		return Outcome{State: StatePending, TxHash: hash.Hex()}, nil
	}
	if receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return Outcome{State: StateFailed, TxHash: hash.Hex(), Reason: "tx.reverted: transaction reverted on-chain"}, nil
	}
	head, err := e.rpc.HeaderByNumber(ctx, nil)
	if err != nil {
		return Outcome{}, fmt.Errorf("latest header: %w", err)
	}
	if head == nil || head.Number == nil || receipt.BlockNumber == nil {
		return Outcome{}, errors.New("latest header has no number")
	}
	depth := new(big.Int).Sub(head.Number, receipt.BlockNumber)
	if depth.Sign() < 0 || depth.Uint64() < e.confs {
		return Outcome{State: StatePending, TxHash: hash.Hex()}, nil
	}
	return Outcome{State: StatePaid, TxHash: hash.Hex()}, nil
}
