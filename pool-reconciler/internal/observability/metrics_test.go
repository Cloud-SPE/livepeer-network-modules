package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordRoundClose(t *testing.T) {
	before := testutil.ToFloat64(roundCloseTotal.WithLabelValues(string(OutcomeClosed)))
	RecordRoundClose(OutcomeClosed, 0.42)
	after := testutil.ToFloat64(roundCloseTotal.WithLabelValues(string(OutcomeClosed)))
	if after != before+1 {
		t.Fatalf("round_close_total delta = %v, want 1", after-before)
	}
	if got := testutil.CollectAndCount(roundCloseDuration); got == 0 {
		t.Fatalf("round_close_duration not observed")
	}
}

func TestRecordPendingRoundRetry(t *testing.T) {
	before := testutil.ToFloat64(pendingRoundsRetried)
	RecordPendingRoundRetry()
	RecordPendingRoundRetry()
	after := testutil.ToFloat64(pendingRoundsRetried)
	if after != before+2 {
		t.Fatalf("pending_rounds_retried delta = %v, want 2", after-before)
	}
}
