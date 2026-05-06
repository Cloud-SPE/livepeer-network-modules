// Package devbroker is a dev-mode Broker. It returns canned SenderInfo
// and accepts redemption submissions without any chain interaction.
//
// Plan 0016 replaces this with a real go-ethereum-backed Broker against
// the on-chain TicketBroker contract.
package devbroker

import (
	"context"
	"math/big"
	"sync"

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers"
)

// DevBroker implements providers.Broker for dev/test runs.
type DevBroker struct {
	mu               sync.Mutex
	defaultDeposit   *big.Int
	defaultReserve   *big.Int
	withdrawRound    int64
	redemptionsCount int
}

// New returns a DevBroker pre-seeded with generous deposit + reserve
// values (1 ETH each) so sender validation never trips on
// no-deposit / no-reserve in dev mode.
func New() *DevBroker {
	one := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil) // 1e18 wei
	return &DevBroker{
		defaultDeposit: new(big.Int).Set(one),
		defaultReserve: new(big.Int).Set(one),
	}
}

// SetWithdrawRound is a test hook to simulate a sender-initiated unlock.
func (b *DevBroker) SetWithdrawRound(round int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.withdrawRound = round
}

// GetSenderInfo returns the canned values; the sender argument is
// ignored.
func (b *DevBroker) GetSenderInfo(_ context.Context, _ []byte) (*providers.SenderInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return &providers.SenderInfo{
		Deposit: new(big.Int).Set(b.defaultDeposit),
		Reserve: &providers.Reserve{
			FundsRemaining: new(big.Int).Set(b.defaultReserve),
			Claimed:        map[string]*big.Int{},
		},
		WithdrawRound: b.withdrawRound,
	}, nil
}

// RedeemWinningTicket records the call and returns nil.
func (b *DevBroker) RedeemWinningTicket(_ context.Context, _, _, _ []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.redemptionsCount++
	return nil
}

// RedemptionsCount returns the number of RedeemWinningTicket calls
// recorded — for tests that want to assert no chain interaction.
func (b *DevBroker) RedemptionsCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.redemptionsCount
}
