package txintent

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/config"
	cerrors "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/errors"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/gasoracle"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/keystore"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/receipts"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// DefaultProcessor drives a TxIntent through the full state machine:
// pending → signed → submitted → mined → confirmed (or → failed). Handles
// nonce assignment, gas-bumping replacement on timeout, reorg recovery via
// the Receipts provider, and failure classification.
//
// One DefaultProcessor instance can drive many intents concurrently (one
// goroutine per intent). Daemons construct it once at startup and pass to
// txintent.New.
type DefaultProcessor struct {
	cfg       config.TxIntentPolicy
	chainID   chain.ChainID
	confs     uint64
	gasLimit  uint64

	rpc       rpc.RPC
	keystore  keystore.Keystore
	gas       gasoracle.GasOracle
	receipts  receipts.Receipts
	clock     clock.Clock
	logger    logger.Logger
	metrics   metrics.Recorder
}

// ProcessorConfig wires a DefaultProcessor.
type ProcessorConfig struct {
	Policy             config.TxIntentPolicy
	ChainID            chain.ChainID
	ReorgConfirmations uint64
	GasLimit           uint64

	RPC      rpc.RPC
	Keystore keystore.Keystore
	Gas      gasoracle.GasOracle
	Receipts receipts.Receipts
	Clock    clock.Clock
	Logger   logger.Logger
	Metrics  metrics.Recorder
}

// NewDefaultProcessor returns a Processor wired with the given dependencies.
func NewDefaultProcessor(c ProcessorConfig) (*DefaultProcessor, error) {
	if c.RPC == nil {
		return nil, fmt.Errorf("txintent processor: RPC is required")
	}
	if c.Keystore == nil {
		return nil, fmt.Errorf("txintent processor: Keystore is required")
	}
	if c.Gas == nil {
		return nil, fmt.Errorf("txintent processor: Gas is required")
	}
	if c.Receipts == nil {
		return nil, fmt.Errorf("txintent processor: Receipts is required")
	}
	if c.ChainID == 0 {
		return nil, fmt.Errorf("txintent processor: ChainID is required")
	}
	if c.ReorgConfirmations == 0 {
		c.ReorgConfirmations = 4
	}
	if c.Clock == nil {
		c.Clock = clock.System()
	}
	if c.Metrics == nil {
		c.Metrics = metrics.NoOp()
	}
	return &DefaultProcessor{
		cfg:      c.Policy,
		chainID:  c.ChainID,
		confs:    c.ReorgConfirmations,
		gasLimit: c.GasLimit,
		rpc:      c.RPC,
		keystore: c.Keystore,
		gas:      c.Gas,
		receipts: c.Receipts,
		clock:    c.Clock,
		logger:   c.Logger,
		metrics:  c.Metrics,
	}, nil
}

// Process implements txintent.Processor. Drives one intent to a terminal
// state and returns. Designed to be invoked in a fresh goroutine per
// intent; concurrent invocations on different intents are safe.
func (p *DefaultProcessor) Process(ctx context.Context, m *Manager, id IntentID) {
	for attempt := 0; attempt <= p.cfg.MaxReplacements; attempt++ {
		intent, err := m.Status(ctx, id)
		if err != nil {
			p.logf("txintent.processor.read_failed", logger.String("id", id.Hex()), logger.Err(err))
			return
		}
		if intent.Status.IsTerminal() {
			return
		}

		// On entry: drive whatever state the intent is in toward the next.
		switch intent.Status {
		case StatusPending:
			if err := p.signAndBroadcast(ctx, m, intent); err != nil {
				if p.handleSubmitErr(ctx, m, intent, err) {
					return
				}
				// transient — retry the sign-and-broadcast at next attempt
				continue
			}

		case StatusSigned:
			// Daemon was killed between sign and broadcast. Re-broadcast.
			if err := p.rebroadcast(ctx, m, intent); err != nil {
				if p.handleSubmitErr(ctx, m, intent, err) {
					return
				}
				continue
			}

		case StatusSubmitted, StatusMined:
			// already in flight — fall through to wait
		}

		// Wait for confirmation (or replacement timeout, or reorg).
		intent, err = m.Status(ctx, id)
		if err != nil {
			return
		}
		cur := intent.CurrentAttempt()
		if cur == nil {
			// Shouldn't happen post-broadcast, but guard against it.
			return
		}

		outcome, err := p.waitWithTimeout(ctx, cur.SignedTxHash)
		if err != nil {
			// ctx cancellation — terminate cleanly
			return
		}

		switch outcome {
		case waitConfirmed:
			_ = m.MarkConfirmed(id)
			p.metricsCounter("livepeer_chain_txintent_terminal_total",
				metrics.Labels{"kind": intent.Kind, "outcome": "confirmed"})
			return

		case waitReverted:
			reason := cerrors.New(cerrors.ClassReverted, "tx.reverted",
				fmt.Sprintf("transaction %s reverted on-chain", cur.SignedTxHash.Hex()))
			_ = m.MarkFailed(id, reason)
			p.metricsCounter("livepeer_chain_txintent_terminal_total",
				metrics.Labels{"kind": intent.Kind, "outcome": "reverted"})
			return

		case waitReorged:
			// Resubmit at same nonce. The TxIntent's current attempt was
			// reorged out; we transition status back to submitted by
			// re-broadcasting the same signed tx.
			p.logf("txintent.processor.reorged",
				logger.String("id", id.Hex()), logger.String("tx", cur.SignedTxHash.Hex()))
			if err := p.rebroadcastFromAttempt(ctx, m, intent, *cur); err != nil {
				if p.handleSubmitErr(ctx, m, intent, err) {
					return
				}
			}
			continue

		case waitTimeout:
			// Replacement: sign a new attempt at the same nonce with bumped
			// gas. If we've exhausted MaxReplacements, fail with Transient.
			if attempt >= p.cfg.MaxReplacements {
				reason := cerrors.New(cerrors.ClassTransient, "tx.replacement_exhausted",
					fmt.Sprintf("intent %s exhausted %d replacements without confirmation", id.Hex(), attempt))
				_ = m.MarkFailed(id, reason)
				p.metricsCounter("livepeer_chain_txintent_terminal_total",
					metrics.Labels{"kind": intent.Kind, "outcome": "replacement_exhausted"})
				return
			}
			if err := p.replace(ctx, m, intent, *cur); err != nil {
				if p.handleSubmitErr(ctx, m, intent, err) {
					return
				}
			}
			continue
		}
	}
}

type waitOutcome int

const (
	waitConfirmed waitOutcome = iota
	waitReverted
	waitReorged
	waitTimeout
)

func (p *DefaultProcessor) waitWithTimeout(ctx context.Context, txHash chain.TxHash) (waitOutcome, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, p.cfg.SubmitTimeout)
	defer cancel()

	receipt, err := p.receipts.WaitConfirmed(timeoutCtx, txHash, p.confs)
	if err != nil {
		// If outer ctx is done, propagate; if it was just our timeout, treat
		// as a replacement signal.
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		// Receipts impl may return ctx.DeadlineExceeded under its own internals;
		// treat any timeout as a replacement signal.
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return waitTimeout, nil
		}
		// Other error: log and treat as transient (replacement signal).
		p.logf("txintent.processor.wait_failed", logger.Err(err))
		return waitTimeout, nil
	}
	if receipt == nil {
		return waitTimeout, nil
	}
	if receipt.Reorged {
		return waitReorged, nil
	}
	if receipt.Status == 0 {
		return waitReverted, nil
	}
	if receipt.Confirmed {
		return waitConfirmed, nil
	}
	return waitTimeout, nil
}

func (p *DefaultProcessor) signAndBroadcast(ctx context.Context, m *Manager, intent TxIntent) error {
	nonce, err := p.rpc.PendingNonceAt(ctx, p.keystore.Address())
	if err != nil {
		return cerrors.Wrap(cerrors.ClassTransient, "rpc.pending_nonce_failed", "failed to fetch pending nonce", err)
	}

	est, err := p.gas.Suggest(ctx)
	if err != nil {
		return cerrors.Wrap(cerrors.ClassTransient, "rpc.gas_suggest_failed", "failed to suggest gas", err)
	}

	tx, err := p.signTx(intent, nonce, est.FeeCap, est.TipCap)
	if err != nil {
		return err
	}

	now := p.clock.Now()
	attempt := IntentAttempt{
		Nonce:         nonce,
		GasFeeCap:     copyBig(est.FeeCap),
		GasTipCap:     copyBig(est.TipCap),
		SignedTxHash:  tx.Hash(),
		BroadcastedAt: now,
	}

	if err := m.AppendAttempt(intent.ID, attempt, StatusSigned); err != nil {
		return cerrors.Wrap(cerrors.ClassPermanent, "txintent.append_signed_failed", "failed to persist signed attempt", err)
	}

	if err := p.rpc.SendTransaction(ctx, tx); err != nil {
		return cerrors.Classify(err)
	}

	if err := m.SetStatus(intent.ID, StatusSubmitted); err != nil {
		return cerrors.Wrap(cerrors.ClassPermanent, "txintent.set_submitted_failed", "failed to persist submitted status", err)
	}
	p.metricsCounter("livepeer_chain_txintent_broadcast_total",
		metrics.Labels{"kind": intent.Kind})
	p.logf("txintent.processor.broadcast",
		logger.String("id", intent.ID.Hex()),
		logger.String("kind", intent.Kind),
		logger.String("tx", tx.Hash().Hex()),
		logger.Uint64("nonce", nonce),
	)
	return nil
}

func (p *DefaultProcessor) rebroadcast(ctx context.Context, m *Manager, intent TxIntent) error {
	// signed but not submitted — broadcast the existing signed attempt.
	cur := intent.CurrentAttempt()
	if cur == nil {
		// No attempt persisted; treat like pending.
		return p.signAndBroadcast(ctx, m, intent)
	}
	tx, err := p.rebuildSignedTx(intent, *cur)
	if err != nil {
		return err
	}
	if err := p.rpc.SendTransaction(ctx, tx); err != nil {
		return cerrors.Classify(err)
	}
	return m.SetStatus(intent.ID, StatusSubmitted)
}

func (p *DefaultProcessor) rebroadcastFromAttempt(ctx context.Context, m *Manager, intent TxIntent, prev IntentAttempt) error {
	// Reorg recovery: sign a new tx at the same nonce using the same gas
	// values, append it as a new attempt (which marks the previous attempt
	// replaced), and broadcast. The new tx hash means WaitConfirmed will
	// track the correct receipt on the next iteration.
	tx, err := p.signTx(intent, prev.Nonce, prev.GasFeeCap, prev.GasTipCap)
	if err != nil {
		return err
	}

	now := p.clock.Now()
	newAttempt := IntentAttempt{
		Nonce:         prev.Nonce,
		GasFeeCap:     copyBig(prev.GasFeeCap),
		GasTipCap:     copyBig(prev.GasTipCap),
		SignedTxHash:  tx.Hash(),
		BroadcastedAt: now,
	}
	if err := m.AppendAttempt(intent.ID, newAttempt, StatusSubmitted); err != nil {
		return err
	}

	if err := p.rpc.SendTransaction(ctx, tx); err != nil {
		return cerrors.Classify(err)
	}
	return nil
}

func (p *DefaultProcessor) replace(ctx context.Context, m *Manager, intent TxIntent, prev IntentAttempt) error {
	bump := big.NewInt(int64(100 + p.cfg.ReplacementGasBump))
	denom := big.NewInt(100)

	newFeeCap := new(big.Int).Mul(prev.GasFeeCap, bump)
	newFeeCap.Quo(newFeeCap, denom)

	newTipCap := new(big.Int).Mul(prev.GasTipCap, bump)
	newTipCap.Quo(newTipCap, denom)

	tx, err := p.signTx(intent, prev.Nonce, newFeeCap, newTipCap)
	if err != nil {
		return err
	}

	now := p.clock.Now()
	newAttempt := IntentAttempt{
		Nonce:         prev.Nonce,
		GasFeeCap:     newFeeCap,
		GasTipCap:     newTipCap,
		SignedTxHash:  tx.Hash(),
		BroadcastedAt: now,
	}

	if err := m.AppendAttempt(intent.ID, newAttempt, StatusSubmitted); err != nil {
		return err
	}

	if err := p.rpc.SendTransaction(ctx, tx); err != nil {
		return cerrors.Classify(err)
	}

	p.metricsCounter("livepeer_chain_txintent_replaced_total",
		metrics.Labels{"kind": intent.Kind})
	p.logf("txintent.processor.replaced",
		logger.String("id", intent.ID.Hex()),
		logger.String("tx", tx.Hash().Hex()),
		logger.Uint64("nonce", prev.Nonce),
	)
	return nil
}

// handleSubmitErr inspects err's classification. Permanent errors transition
// the intent to failed (returns true). Transient errors are logged and
// returns false so the caller continues the retry loop.
func (p *DefaultProcessor) handleSubmitErr(ctx context.Context, m *Manager, intent TxIntent, err error) bool {
	classified := cerrors.Classify(err)
	switch classified.Class {
	case cerrors.ClassPermanent, cerrors.ClassReverted, cerrors.ClassNoncePast, cerrors.ClassInsufficientFunds:
		_ = m.MarkFailed(intent.ID, classified)
		p.metricsCounter("livepeer_chain_txintent_terminal_total",
			metrics.Labels{"kind": intent.Kind, "outcome": classified.Class.String()})
		p.logf("txintent.processor.failed",
			logger.String("id", intent.ID.Hex()),
			logger.String("class", classified.Class.String()),
			logger.String("code", classified.Code),
			logger.Err(err),
		)
		return true
	default:
		// Transient: caller will retry.
		p.logf("txintent.processor.transient_error",
			logger.String("id", intent.ID.Hex()),
			logger.String("code", classified.Code),
			logger.Err(err),
		)
		// Avoid tight retry loops: brief sleep before caller re-enters.
		_ = p.clock.Sleep(ctx, 500*time.Millisecond)
		return false
	}
}

func (p *DefaultProcessor) signTx(intent TxIntent, nonce uint64, feeCap, tipCap *big.Int) (*ethtypes.Transaction, error) {
	gasLimit := intent.GasLimit
	if gasLimit == 0 {
		gasLimit = p.gasLimit
	}
	tx := ethtypes.NewTx(&ethtypes.DynamicFeeTx{
		ChainID:   p.chainID.BigInt(),
		Nonce:     nonce,
		GasTipCap: copyBig(tipCap),
		GasFeeCap: copyBig(feeCap),
		Gas:       gasLimit,
		To:        toAddrPtr(intent.To),
		Value:     copyBig(intent.Value),
		Data:      append([]byte(nil), intent.CallData...),
	})
	signed, err := p.keystore.SignTx(tx, p.chainID)
	if err != nil {
		return nil, cerrors.Wrap(cerrors.ClassPermanent, "tx.sign_failed", "failed to sign transaction", err)
	}
	return signed, nil
}

func (p *DefaultProcessor) rebuildSignedTx(intent TxIntent, prev IntentAttempt) (*ethtypes.Transaction, error) {
	return p.signTx(intent, prev.Nonce, prev.GasFeeCap, prev.GasTipCap)
}

func (p *DefaultProcessor) logf(msg string, fields ...logger.Field) {
	if p.logger == nil {
		return
	}
	p.logger.Info(msg, fields...)
}

func (p *DefaultProcessor) metricsCounter(name string, labels metrics.Labels) {
	if p.metrics == nil {
		return
	}
	p.metrics.CounterAdd(name, labels, 1)
}

func copyBig(b *big.Int) *big.Int {
	if b == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(b)
}

func toAddrPtr(a chain.Address) *chain.Address {
	out := a
	return &out
}

// Compile-time: DefaultProcessor implements Processor.
var _ Processor = (*DefaultProcessor)(nil)
