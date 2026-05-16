package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func TestRecordMetadataRefresh(t *testing.T) {
	attemptedAt := time.Unix(1710000000, 0).UTC()
	successAt := attemptedAt.Add(2 * time.Second)
	before := testutil.ToFloat64(metadataRefreshTotal.WithLabelValues("vtuber", "vtuber-runner", "enriched"))

	RecordMetadataRefresh(
		"vtuber",
		"livepeer:vtuber-session",
		"vtuber-default",
		"vtuber-runner",
		"enriched",
		"",
		0.25,
		attemptedAt,
		successAt,
	)

	after := testutil.ToFloat64(metadataRefreshTotal.WithLabelValues("vtuber", "vtuber-runner", "enriched"))
	if after != before+1 {
		t.Fatalf("counter delta = %v; want %v", after-before, 1.0)
	}

	if got := testutil.ToFloat64(metadataRefreshLastAttemptTimestamp.WithLabelValues(
		"vtuber",
		"livepeer:vtuber-session",
		"vtuber-default",
		"vtuber-runner",
	)); got != float64(attemptedAt.Unix()) {
		t.Fatalf("last attempt timestamp = %v; want %v", got, float64(attemptedAt.Unix()))
	}

	if got := testutil.ToFloat64(metadataRefreshLastSuccessTimestamp.WithLabelValues(
		"vtuber",
		"livepeer:vtuber-session",
		"vtuber-default",
		"vtuber-runner",
	)); got != float64(successAt.Unix()) {
		t.Fatalf("last success timestamp = %v; want %v", got, float64(successAt.Unix()))
	}

	observer, err := metadataRefreshDuration.GetMetricWithLabelValues("vtuber", "vtuber-runner", "enriched")
	if err != nil {
		t.Fatalf("get histogram metric: %v", err)
	}
	metric, ok := observer.(prometheus.Metric)
	if !ok {
		t.Fatal("histogram observer does not implement prometheus.Metric")
	}
	histogram := &dto.Metric{}
	if err := metric.Write(histogram); err != nil {
		t.Fatalf("write histogram: %v", err)
	}
	if got := histogram.GetHistogram().GetSampleCount(); got < 1 {
		t.Fatalf("histogram sample count = %d; want at least 1", got)
	}

	if got := testutil.ToFloat64(metadataRefreshCurrentResult.WithLabelValues(
		"vtuber",
		"livepeer:vtuber-session",
		"vtuber-default",
		"vtuber-runner",
		"enriched",
	)); got != 1 {
		t.Fatalf("current result gauge = %v; want 1", got)
	}
}

func TestRecordMetadataRefresh_NormalizesEmptyLabels(t *testing.T) {
	attemptedAt := time.Unix(1710000100, 0).UTC()

	before := testutil.ToFloat64(metadataRefreshTotal.WithLabelValues("other", "unknown", "unknown"))

	RecordMetadataRefresh("other", "", "", "", "", "", 0.1, attemptedAt, time.Time{})

	after := testutil.ToFloat64(metadataRefreshTotal.WithLabelValues("other", "unknown", "unknown"))
	if after != before+1 {
		t.Fatalf("counter delta = %v; want %v", after-before, 1.0)
	}

	if got := testutil.ToFloat64(metadataRefreshLastAttemptTimestamp.WithLabelValues(
		"other",
		"unknown",
		"unknown",
		"unknown",
	)); got != float64(attemptedAt.Unix()) {
		t.Fatalf("last attempt timestamp = %v; want %v", got, float64(attemptedAt.Unix()))
	}

	if got := testutil.ToFloat64(metadataRefreshCurrentResult.WithLabelValues(
		"other",
		"unknown",
		"unknown",
		"unknown",
		"unknown",
	)); got != 1 {
		t.Fatalf("current result gauge = %v; want 1", got)
	}
}

func TestRecordMetadataRefresh_ResetsPreviousCurrentResult(t *testing.T) {
	attemptedAt := time.Unix(1710000200, 0).UTC()

	RecordMetadataRefresh(
		"openai",
		"openai:chat-completions",
		"chat-default",
		"vllm",
		"enriched",
		"",
		0.1,
		attemptedAt,
		attemptedAt,
	)

	RecordMetadataRefresh(
		"openai",
		"openai:chat-completions",
		"chat-default",
		"vllm",
		"model_not_found",
		"enriched",
		0.1,
		attemptedAt.Add(time.Minute),
		time.Time{},
	)

	if got := testutil.ToFloat64(metadataRefreshCurrentResult.WithLabelValues(
		"openai",
		"openai:chat-completions",
		"chat-default",
		"vllm",
		"enriched",
	)); got != 0 {
		t.Fatalf("previous result gauge = %v; want 0", got)
	}

	if got := testutil.ToFloat64(metadataRefreshCurrentResult.WithLabelValues(
		"openai",
		"openai:chat-completions",
		"chat-default",
		"vllm",
		"model_not_found",
	)); got != 1 {
		t.Fatalf("new result gauge = %v; want 1", got)
	}
}
