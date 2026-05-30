package lifecycle

import (
	"context"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/roundclock"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/service/bondingadmin"
)

// LockedActions is the subset of the bondingadmin service the locked-action
// runner drives. Each method runs the once-per-round handler for a round
// and is internally idempotent (durable txintent key) and gated
// (locked+initialized) — the runner only decides WHEN to attempt.
type LockedActions interface {
	TransferBond(ctx context.Context, round chain.Round) (bondingadmin.ActionResult, error)
	WithdrawFees(ctx context.Context, round chain.Round) (bondingadmin.ActionResult, error)
}

// LockReader reads RoundsManager.roundLockAmount (in L1 blocks). Used to
// compute the deterministic lock block from a round event.
type LockReader interface {
	RoundLockAmount(ctx context.Context) (chain.BlockNumber, error)
}

// runLockedActions drives transfer-bond and withdraw-fees off the round +
// L1-block streams instead of polling currentRoundLocked. On each new round
// it computes lockBlock = L1StartBlock + Length − roundLockAmount; once the
// L1 block crosses that estimate it attempts both actions. The estimate is
// only a trigger — bondingadmin re-reads the authoritative locked state
// before every submit, so an early trigger is harmless (it returns a
// round-not-locked skip and is retried on the next block).
//
// Per-round in-memory "fired" guards stop retry spam once an action reaches
// a terminal outcome for the round; the durable txintent key prevents a
// successful double-submit across restarts even though the guards reset.
func runLockedActions(ctx context.Context, rc roundclock.Clock, actions LockedActions, lockReader LockReader, log logger.Logger) error {
	rounds, err := rc.SubscribeRounds(ctx)
	if err != nil {
		return err
	}
	blocks, err := rc.SubscribeL1Blocks(ctx)
	if err != nil {
		return err
	}

	var (
		cur        chain.Round
		haveRound  bool
		lockBlock  chain.BlockNumber
		firedXfer  = map[chain.RoundNumber]bool{}
		firedWdraw = map[chain.RoundNumber]bool{}
	)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case r, ok := <-rounds:
			if !ok {
				return nil
			}
			cur = r
			haveRound = true
			lockAmount, err := lockReader.RoundLockAmount(ctx)
			if err != nil {
				// Without the lock amount we can't compute the trigger;
				// log and wait for the next round event to retry.
				if log != nil {
					log.Warn("locked-actions: roundLockAmount read failed", logger.Err(err))
				}
				haveRound = false
				continue
			}
			lockBlock = lockTriggerBlock(r, lockAmount)
			// Bound the fired maps: keep only the current round.
			pruneFired(firedXfer, r.Number)
			pruneFired(firedWdraw, r.Number)

		case b, ok := <-blocks:
			if !ok {
				return nil
			}
			if !haveRound || b < lockBlock {
				continue
			}
			if !firedXfer[cur.Number] {
				if terminal := attempt(ctx, "transfer-bond", cur, actions.TransferBond, log); terminal {
					firedXfer[cur.Number] = true
				}
			}
			if !firedWdraw[cur.Number] {
				if terminal := attempt(ctx, "withdraw-fees", cur, actions.WithdrawFees, log); terminal {
					firedWdraw[cur.Number] = true
				}
			}
		}
	}
}

// lockTriggerBlock returns the L1 block at which the round's lock window
// opens: L1StartBlock + Length − roundLockAmount.
func lockTriggerBlock(r chain.Round, lockAmount chain.BlockNumber) chain.BlockNumber {
	end := r.L1StartBlock + r.Length
	if lockAmount > end {
		return r.L1StartBlock
	}
	return end - lockAmount
}

// attempt runs one action handler and reports whether the round reached a
// terminal outcome (submitted, or a skip that won't resolve this round) so
// the caller stops retrying. Retryable cases (not-yet-locked, reward-not-
// confirmed, transient error) return false so a later block re-attempts.
func attempt(ctx context.Context, name string, round chain.Round, fn func(context.Context, chain.Round) (bondingadmin.ActionResult, error), log logger.Logger) bool {
	res, err := fn(ctx, round)
	if err != nil {
		if log != nil {
			log.Warn("locked-actions: attempt failed; will retry next block",
				logger.String("action", name),
				logger.Uint64("round", uint64(round.Number)),
				logger.Err(err))
		}
		return false
	}
	if res.Skip == nil {
		if log != nil {
			log.Info("locked-actions: submitted",
				logger.String("action", name),
				logger.Uint64("round", uint64(round.Number)),
				logger.String("intent_id", res.IntentID.Hex()))
		}
		return true
	}
	switch res.Skip.Code {
	case bondingadmin.SkipCodeRoundNotLocked, bondingadmin.SkipCodeRewardNotCalled:
		// Trigger fired before the window truly opened, or reward not yet
		// confirmed; retry on a later block.
		return false
	default:
		// Disabled / nothing-to-transfer / below-threshold: nothing more to
		// do for this round.
		if log != nil {
			log.Debug("locked-actions: skipped (terminal for round)",
				logger.String("action", name),
				logger.Uint64("round", uint64(round.Number)),
				logger.String("reason", res.Skip.Reason))
		}
		return true
	}
}

// pruneFired drops every entry except the current round, keeping the maps
// from growing without bound over the daemon's lifetime.
func pruneFired(m map[chain.RoundNumber]bool, keep chain.RoundNumber) {
	for k := range m {
		if k != keep {
			delete(m, k)
		}
	}
}
