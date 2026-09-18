package devbroker

import (
	"context"
	"math/big"
	"testing"
)

func TestDevBrokerLifecycle(t *testing.T) {
	b := New()
	b.SetWithdrawRound(17)
	info, err := b.GetSenderInfo(context.Background(), []byte("ignored"))
	if err != nil || info.WithdrawRound != 17 || info.Deposit.Sign() <= 0 || info.Reserve.FundsRemaining.Sign() <= 0 {
		t.Fatalf("sender info=%+v err=%v", info, err)
	}
	info.Deposit.SetInt64(0)
	again, _ := b.GetSenderInfo(context.Background(), nil)
	if again.Deposit.Sign() == 0 {
		t.Fatal("GetSenderInfo leaked mutable deposit state")
	}
	if used, err := b.IsUsedTicket(context.Background(), nil); err != nil || used {
		t.Fatalf("IsUsedTicket=%v,%v", used, err)
	}
	for i := byte(1); i <= 2; i++ {
		hash, err := b.RedeemWinningTicket(context.Background(), nil, nil, big.NewInt(1))
		if err != nil || len(hash) != 32 || hash[31] != i {
			t.Fatalf("redemption %d hash=%x err=%v", i, hash, err)
		}
	}
	if b.RedemptionsCount() != 2 {
		t.Fatalf("redemptions=%d", b.RedemptionsCount())
	}
	if period, err := b.TicketValidityPeriod(context.Background()); err != nil || period != 2 {
		t.Fatalf("validity=%d err=%v", period, err)
	}
}
