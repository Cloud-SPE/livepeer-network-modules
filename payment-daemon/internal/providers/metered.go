package providers

import (
	"context"
	"math/big"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
)

// Chain method labels for the metered Broker.
const (
	chainGetSenderInfo       = "get_sender_info"
	chainIsUsedTicket        = "is_used_ticket"
	chainRedeemWinningTicket = "redeem_winning_ticket"
)

// meteredBroker wraps a Broker and records chain read/write counts,
// latency, and last-success timestamp. It is transparent: arguments and
// results pass through untouched.
type meteredBroker struct {
	inner Broker
	rec   metrics.Recorder
}

// NewMeteredBroker decorates a Broker with chain metrics. A nil inner
// returns nil; a nil recorder yields a no-op recorder.
func NewMeteredBroker(inner Broker, rec metrics.Recorder) Broker {
	if inner == nil {
		return nil
	}
	if rec == nil {
		rec = metrics.NewNoop()
	}
	return &meteredBroker{inner: inner, rec: rec}
}

func chainResult(err error) string {
	if err != nil {
		return metrics.ResultError
	}
	return metrics.ResultOK
}

func (m *meteredBroker) GetSenderInfo(ctx context.Context, sender []byte) (*SenderInfo, error) {
	start := time.Now()
	info, err := m.inner.GetSenderInfo(ctx, sender)
	m.rec.ObserveChainRead(chainGetSenderInfo, time.Since(start))
	m.rec.IncChainRead(chainGetSenderInfo, chainResult(err))
	if err == nil {
		m.rec.SetChainLastSuccess(time.Now())
	}
	return info, err
}

func (m *meteredBroker) IsUsedTicket(ctx context.Context, ticketHash []byte) (bool, error) {
	start := time.Now()
	used, err := m.inner.IsUsedTicket(ctx, ticketHash)
	m.rec.ObserveChainRead(chainIsUsedTicket, time.Since(start))
	m.rec.IncChainRead(chainIsUsedTicket, chainResult(err))
	if err == nil {
		m.rec.SetChainLastSuccess(time.Now())
	}
	return used, err
}

func (m *meteredBroker) RedeemWinningTicket(ctx context.Context, ticket *Ticket, sig []byte, recipientRand *big.Int) ([]byte, error) {
	txHash, err := m.inner.RedeemWinningTicket(ctx, ticket, sig, recipientRand)
	m.rec.IncChainWrite(chainRedeemWinningTicket, chainResult(err))
	if err == nil {
		m.rec.SetChainLastSuccess(time.Now())
	}
	return txHash, err
}

// Compile-time interface check.
var _ Broker = (*meteredBroker)(nil)

// TicketValidityPeriod passes through; it is a startup read, not a
// per-request one, so it carries no metrics of its own.
func (m *meteredBroker) TicketValidityPeriod(ctx context.Context) (int64, error) {
	return m.inner.TicketValidityPeriod(ctx)
}
