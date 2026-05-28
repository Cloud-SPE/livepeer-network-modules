package settlement

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
)

func TestRedemptionResultLabel(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, metrics.RedeemRedeemed},
		{ErrTicketExpired, metrics.RedeemExpired},
		{ErrTicketUsed, metrics.RedeemAlreadyUsed},
		{ErrFaceValueTooLow, metrics.RedeemFaceValueTooLow},
		{ErrInsufficientFunds, metrics.RedeemInsufficientFund},
		{fmt.Errorf("redeem: %w", errors.New("rpc down")), metrics.RedeemTxError},
		{errors.New("anything else"), metrics.RedeemTxError},
	}
	for _, c := range cases {
		if got := redemptionResultLabel(c.err); got != c.want {
			t.Errorf("redemptionResultLabel(%v): want %q, got %q", c.err, c.want, got)
		}
	}
}
