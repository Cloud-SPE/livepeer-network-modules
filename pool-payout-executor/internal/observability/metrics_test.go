package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordTransactionSubmitted(t *testing.T) {
	before := testutil.ToFloat64(transactionSubmittedTotal.WithLabelValues("succeeded"))
	RecordTransactionSubmitted("succeeded")
	RecordTransactionSubmitted("failed")
	RecordTransactionSubmitted("") // empty maps to "unknown"
	after := testutil.ToFloat64(transactionSubmittedTotal.WithLabelValues("succeeded"))
	if after != before+1 {
		t.Fatalf("succeeded delta = %v, want 1", after-before)
	}
	if got := testutil.ToFloat64(transactionSubmittedTotal.WithLabelValues("unknown")); got < 1 {
		t.Fatalf("unknown label not recorded; got %v", got)
	}
}

func TestRecordTransactionConfirmed(t *testing.T) {
	before := testutil.ToFloat64(transactionConfirmedTotal.WithLabelValues("succeeded"))
	RecordTransactionConfirmed("succeeded")
	after := testutil.ToFloat64(transactionConfirmedTotal.WithLabelValues("succeeded"))
	if after != before+1 {
		t.Fatalf("succeeded delta = %v, want 1", after-before)
	}
}

func TestRecordReconcileIteration(t *testing.T) {
	before := testutil.ToFloat64(reconcileIterationTotal.WithLabelValues("success"))
	RecordReconcileIteration("success", 0.5)
	after := testutil.ToFloat64(reconcileIterationTotal.WithLabelValues("success"))
	if after != before+1 {
		t.Fatalf("success delta = %v, want 1", after-before)
	}
	if got := testutil.CollectAndCount(reconcileIterationDuration); got == 0 {
		t.Fatalf("reconcile_iteration_duration not observed")
	}
}
