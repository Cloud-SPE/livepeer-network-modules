package providers

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
)

type stubBroker struct {
	getErr    error
	redeemErr error
}

func (s *stubBroker) GetSenderInfo(context.Context, []byte) (*SenderInfo, error) {
	return &SenderInfo{}, s.getErr
}
func (s *stubBroker) IsUsedTicket(context.Context, []byte) (bool, error) { return false, nil }
func (s *stubBroker) RedeemWinningTicket(context.Context, *Ticket, []byte, *big.Int) ([]byte, error) {
	return nil, s.redeemErr
}

func TestNewMeteredBrokerNilPassthrough(t *testing.T) {
	if NewMeteredBroker(nil, nil) != nil {
		t.Fatal("nil inner should return nil")
	}
}

func TestMeteredBrokerRecordsReadsAndWrites(t *testing.T) {
	rec := metrics.NewPrometheus()
	b := NewMeteredBroker(&stubBroker{redeemErr: errors.New("rpc down")}, rec)

	ctx := context.Background()
	_, _ = b.GetSenderInfo(ctx, []byte{0x01})
	_, _ = b.IsUsedTicket(ctx, []byte{0x02})
	_, _ = b.RedeemWinningTicket(ctx, &Ticket{}, nil, nil)

	const want = `
# HELP livepeer_payment_chain_reads_total Chain read calls, labeled by method and result.
# TYPE livepeer_payment_chain_reads_total counter
livepeer_payment_chain_reads_total{method="get_sender_info",result="ok"} 1
livepeer_payment_chain_reads_total{method="is_used_ticket",result="ok"} 1
# HELP livepeer_payment_chain_writes_total Chain write calls, labeled by method and result.
# TYPE livepeer_payment_chain_writes_total counter
livepeer_payment_chain_writes_total{method="redeem_winning_ticket",result="error"} 1
`
	if err := testutil.GatherAndCompare(rec.Registry(), strings.NewReader(want),
		"livepeer_payment_chain_reads_total",
		"livepeer_payment_chain_writes_total",
	); err != nil {
		t.Fatalf("chain metrics mismatch: %v", err)
	}
}
