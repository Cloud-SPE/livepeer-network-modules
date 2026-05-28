package receiver

import (
	"math/big"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
)

func TestRecordPaymentMetrics(t *testing.T) {
	rec := metrics.NewPrometheus()
	s := &Service{metrics: rec}

	statuses := []*pb.TicketStatus{
		{}, // accepted
		{RejectionReason: pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_NONCE_REPLAY},
		{RejectionReason: pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_INVALID_SIGNATURE},
	}
	s.recordPaymentMetrics(statuses, 2, big.NewInt(3_000_000_000)) // 3 gwei

	const want = `
# HELP livepeer_payment_credited_ev_gwei_total Cumulative credited expected value in gwei.
# TYPE livepeer_payment_credited_ev_gwei_total counter
livepeer_payment_credited_ev_gwei_total 3
# HELP livepeer_payment_tickets_rejected_total Rejected tickets, labeled by reason.
# TYPE livepeer_payment_tickets_rejected_total counter
livepeer_payment_tickets_rejected_total{reason="invalid_signature"} 1
livepeer_payment_tickets_rejected_total{reason="nonce_replay"} 1
# HELP livepeer_payment_tickets_total Processed tickets, labeled by result (accepted/rejected).
# TYPE livepeer_payment_tickets_total counter
livepeer_payment_tickets_total{result="accepted"} 1
livepeer_payment_tickets_total{result="rejected"} 2
# HELP livepeer_payment_winning_tickets_total Winning tickets queued for redemption.
# TYPE livepeer_payment_winning_tickets_total counter
livepeer_payment_winning_tickets_total 2
`
	if err := testutil.GatherAndCompare(rec.Registry(), strings.NewReader(want),
		"livepeer_payment_credited_ev_gwei_total",
		"livepeer_payment_tickets_rejected_total",
		"livepeer_payment_tickets_total",
		"livepeer_payment_winning_tickets_total",
	); err != nil {
		t.Fatalf("metrics mismatch: %v", err)
	}
}

func TestRejectionReasonLabel(t *testing.T) {
	cases := map[pb.PaymentRejectionReason]string{
		pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_INVALID_RECIPIENT_RAND: metrics.ReasonInvalidRecipientRand,
		pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_NONCE_REPLAY:           metrics.ReasonNonceReplay,
		pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_NONCE_CAP_REACHED:      metrics.ReasonNonceCap,
		pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_INVALID_SIGNATURE:      metrics.ReasonInvalidSignature,
		pb.PaymentRejectionReason_PAYMENT_REJECTION_REASON_UNSPECIFIED:            metrics.ReasonOther,
	}
	for in, want := range cases {
		if got := rejectionReasonLabel(in); got != want {
			t.Errorf("rejectionReasonLabel(%v): want %q, got %q", in, want, got)
		}
	}
}
