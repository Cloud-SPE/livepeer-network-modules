package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordMetadataRefresh(t *testing.T) {
	before := testutil.ToFloat64(metadataRefreshTotal.WithLabelValues("vtuber", "vtuber-runner", "enriched"))

	RecordMetadataRefresh("vtuber", "vtuber-runner", "enriched")

	after := testutil.ToFloat64(metadataRefreshTotal.WithLabelValues("vtuber", "vtuber-runner", "enriched"))
	if after != before+1 {
		t.Fatalf("counter delta = %v; want %v", after-before, 1.0)
	}
}
