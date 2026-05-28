package escrow

import (
	"math/big"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
)

func TestEscrowGauges(t *testing.T) {
	rec := metrics.NewPrometheus()
	e := New(nil, nil, Config{Recorder: rec})

	senderA := []byte{0xaa}
	senderB := []byte{0xbb}
	e.SubFloat(senderA, big.NewInt(100))
	e.SubFloat(senderB, big.NewInt(50))
	// Releasing all of B's float should drop tracked senders back to 1.
	if err := e.AddFloat(senderB, big.NewInt(50)); err != nil {
		t.Fatalf("AddFloat: %v", err)
	}

	const want = `
# HELP livepeer_payment_escrow_pending_float_wei Total pending escrow float across all tracked senders (wei).
# TYPE livepeer_payment_escrow_pending_float_wei gauge
livepeer_payment_escrow_pending_float_wei 100
# HELP livepeer_payment_tracked_senders Number of senders escrow is currently tracking.
# TYPE livepeer_payment_tracked_senders gauge
livepeer_payment_tracked_senders 1
`
	if err := testutil.GatherAndCompare(rec.Registry(), strings.NewReader(want),
		"livepeer_payment_escrow_pending_float_wei",
		"livepeer_payment_tracked_senders",
	); err != nil {
		t.Fatalf("escrow gauges mismatch: %v", err)
	}
}
