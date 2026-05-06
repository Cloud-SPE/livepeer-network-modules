// Package escrow computes per-sender ceilings on ticket face_value
// (MaxFloat) and tracks the "pending" face value of in-flight
// redemptions so MaxFloat reflects committed-but-unconfirmed funds.
//
// Per the runbook §3:
//
//	if pendingAmount == 0:
//	  maxFloat = reserveAlloc + deposit
//	else if (deposit / pendingAmount) ≥ 3.0:
//	  maxFloat = reserveAlloc + deposit            # ignore pending
//	else:
//	  maxFloat = reserveAlloc + deposit - pendingAmount
//
// reserveAlloc = (reserve.totalFunds / poolSize) - claimedByMe.
//
// Pending is in-memory, keyed by sender. Settlement calls SubFloat
// (reserve in-flight) and AddFloat (release) around each redemption.
// On receiver restart, pending must be rebuilt from the redemptions
// queue — see Rebuild.
package escrow

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/store"
)

// MinDepositPendingRatio: when deposit / pendingAmount ≥ this, the
// pendingAmount is ignored in MaxFloat computation. Documented in the
// operator runbook.
const MinDepositPendingRatio = 3

// ErrPendingUnderflow is returned when AddFloat would drop pending
// below zero.
var ErrPendingUnderflow = errors.New("escrow: pending underflow")

// Config holds the escrow service's tunable state.
type Config struct {
	// Claimant is the receiver's claim address (orch identity / hot
	// signer). Used to subtract claimed-by-us from reserveAlloc.
	Claimant []byte
}

// Escrow is the off-chain accounting service.
type Escrow struct {
	broker providers.Broker
	clock  providers.Clock
	cfg    Config

	mu      sync.Mutex
	pending map[string]*big.Int // hex(senderAddr) -> pending wei
}

// New constructs an Escrow.
func New(broker providers.Broker, clock providers.Clock, cfg Config) *Escrow {
	return &Escrow{
		broker:  broker,
		clock:   clock,
		cfg:     cfg,
		pending: map[string]*big.Int{},
	}
}

// MaxFloat returns the max face_value safely committable to a single
// ticket from `sender`.
func (e *Escrow) MaxFloat(ctx context.Context, sender []byte) (*big.Int, error) {
	info, err := e.broker.GetSenderInfo(ctx, sender)
	if err != nil {
		return nil, err
	}
	reserveAlloc := e.reserveAlloc(info)
	deposit := nilToZero(info.Deposit)

	pending := e.getPending(sender)
	if pending.Sign() == 0 {
		return new(big.Int).Add(reserveAlloc, deposit), nil
	}

	// 3:1 heuristic — if deposit/pending ≥ 3, ignore pending.
	threshold := new(big.Int).Mul(pending, big.NewInt(MinDepositPendingRatio))
	if deposit.Cmp(threshold) >= 0 {
		return new(big.Int).Add(reserveAlloc, deposit), nil
	}
	full := new(big.Int).Add(reserveAlloc, deposit)
	return new(big.Int).Sub(full, pending), nil
}

// AvailableFunds returns the total funds that could cover redemptions
// for the sender: reserveAlloc + deposit - pending. Used by
// settlement's gas-cost preflight.
func (e *Escrow) AvailableFunds(ctx context.Context, sender []byte) (*big.Int, error) {
	info, err := e.broker.GetSenderInfo(ctx, sender)
	if err != nil {
		return nil, err
	}
	reserveAlloc := e.reserveAlloc(info)
	deposit := nilToZero(info.Deposit)
	pending := e.getPending(sender)
	full := new(big.Int).Add(reserveAlloc, deposit)
	return new(big.Int).Sub(full, pending), nil
}

// SubFloat reserves `amount` as pending for the sender.
func (e *Escrow) SubFloat(sender []byte, amount *big.Int) {
	if amount == nil || amount.Sign() == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.getPendingLocked(sender)
	p.Add(p, amount)
}

// AddFloat releases `amount` of pending for the sender. Returns
// ErrPendingUnderflow if the bookkeeping would go below zero (settlement
// invariant violation; logged + metric in the caller).
func (e *Escrow) AddFloat(sender []byte, amount *big.Int) error {
	if amount == nil || amount.Sign() == 0 {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.getPendingLocked(sender)
	if p.Cmp(amount) < 0 {
		return ErrPendingUnderflow
	}
	p.Sub(p, amount)
	return nil
}

// Pending returns the current pending amount for sender. Test-only.
func (e *Escrow) Pending(sender []byte) *big.Int {
	return new(big.Int).Set(e.getPending(sender))
}

// Rebuild seeds pending from the redemptions store. Called on receiver
// boot so a restart doesn't lose the pending bookkeeping.
func (e *Escrow) Rebuild(st *store.Store) error {
	pend, err := st.PendingRedemptions()
	if err != nil {
		return fmt.Errorf("escrow rebuild: %w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range pend {
		if p.Ticket == nil || p.Ticket.FaceValue == nil {
			continue
		}
		key := strings.ToLower(fmt.Sprintf("%x", p.Ticket.Sender))
		v, ok := e.pending[key]
		if !ok {
			v = new(big.Int)
			e.pending[key] = v
		}
		v.Add(v, p.Ticket.FaceValue)
	}
	return nil
}

// reserveAlloc = (reserve.totalFunds / poolSize) - claimedByMe.
// totalFunds = FundsRemaining + sum(Claimed) — but our broker only
// fills Claimed[claimant] = our claim, so totalFunds = FundsRemaining +
// Claimed[claimant].
func (e *Escrow) reserveAlloc(info *providers.SenderInfo) *big.Int {
	if info == nil || info.Reserve == nil {
		return new(big.Int)
	}
	pool := e.clock.GetTranscoderPoolSize()
	if pool == nil || pool.Sign() == 0 {
		return new(big.Int)
	}
	total := new(big.Int).Set(nilToZero(info.Reserve.FundsRemaining))
	claimedByMe := new(big.Int)
	if info.Reserve.Claimed != nil && len(e.cfg.Claimant) > 0 {
		key := "0x" + strings.ToLower(fmt.Sprintf("%x", e.cfg.Claimant))
		if v, ok := info.Reserve.Claimed[key]; ok && v != nil {
			claimedByMe = new(big.Int).Set(v)
			total.Add(total, claimedByMe)
		}
	}
	return new(big.Int).Sub(new(big.Int).Quo(total, pool), claimedByMe)
}

func (e *Escrow) getPending(sender []byte) *big.Int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return new(big.Int).Set(e.getPendingLocked(sender))
}

func (e *Escrow) getPendingLocked(sender []byte) *big.Int {
	key := strings.ToLower(fmt.Sprintf("%x", sender))
	v, ok := e.pending[key]
	if !ok {
		v = new(big.Int)
		e.pending[key] = v
	}
	return v
}

func nilToZero(v *big.Int) *big.Int {
	if v == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(v)
}
