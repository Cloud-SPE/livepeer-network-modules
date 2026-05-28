package payment

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
)

func TestWithMetricsNilPassthrough(t *testing.T) {
	if WithMetrics(nil) != nil {
		t.Fatal("WithMetrics(nil) should return nil")
	}
}

func TestMeteredRecordsSuccessAndErrorResults(t *testing.T) {
	okBefore := testutil.ToFloat64(observability.TestPaymentClientRequestsCounter("process_payment", "ok"))
	errBefore := testutil.ToFloat64(observability.TestPaymentClientRequestsCounter("open_session", codes.Unavailable.String()))

	client := WithMetrics(&stubClient{
		openErr: status.Error(codes.Unavailable, "daemon down"),
	})

	if _, err := client.ProcessPayment(context.Background(), ProcessPaymentRequest{}); err != nil {
		t.Fatalf("ProcessPayment: unexpected error %v", err)
	}
	if _, err := client.OpenSession(context.Background(), OpenSessionRequest{}); err == nil {
		t.Fatal("OpenSession: expected error")
	}

	okAfter := testutil.ToFloat64(observability.TestPaymentClientRequestsCounter("process_payment", "ok"))
	errAfter := testutil.ToFloat64(observability.TestPaymentClientRequestsCounter("open_session", codes.Unavailable.String()))

	if okAfter-okBefore != 1 {
		t.Fatalf("process_payment ok counter: want +1, got %v", okAfter-okBefore)
	}
	if errAfter-errBefore != 1 {
		t.Fatalf("open_session Unavailable counter: want +1, got %v", errAfter-errBefore)
	}
}

func TestResultLabel(t *testing.T) {
	if got := resultLabel(nil); got != "ok" {
		t.Fatalf("nil err: want ok, got %q", got)
	}
	if got := resultLabel(status.Error(codes.DeadlineExceeded, "x")); got != codes.DeadlineExceeded.String() {
		t.Fatalf("grpc err: want %q, got %q", codes.DeadlineExceeded.String(), got)
	}
	if got := resultLabel(errors.New("plain")); got != codes.Unknown.String() {
		t.Fatalf("plain err: want %q, got %q", codes.Unknown.String(), got)
	}
}

// stubClient is a minimal Client whose calls can be made to fail per-method.
type stubClient struct {
	Client
	openErr error
}

func (s *stubClient) ProcessPayment(context.Context, ProcessPaymentRequest) (*ProcessPaymentResult, error) {
	return &ProcessPaymentResult{}, nil
}

func (s *stubClient) OpenSession(context.Context, OpenSessionRequest) (*OpenSessionResult, error) {
	return nil, s.openErr
}
