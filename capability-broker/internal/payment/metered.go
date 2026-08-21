package payment

import (
	"context"
	"math/big"

	"google.golang.org/grpc/status"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
)

// metered wraps a Client and records per-RPC Prometheus metrics
// (livepeer_payment_client_*) for every call. It is transparent: it forwards
// arguments and results untouched and only observes timing + outcome.
type metered struct {
	inner Client
}

// WithMetrics decorates a Client so every RPC reports request count, duration,
// and in-flight gauge to the broker's observability registry. The result label
// is "ok" on success or the gRPC status code (e.g. "Unavailable") on error.
func WithMetrics(inner Client) Client {
	if inner == nil {
		return nil
	}
	return &metered{inner: inner}
}

// resultLabel maps an RPC error to a bounded label value: "ok" on success,
// otherwise the gRPC status code string. Non-gRPC errors map to "Unknown".
func resultLabel(err error) string {
	if err == nil {
		return "ok"
	}
	return status.Code(err).String()
}

func (m *metered) GetTicketParams(ctx context.Context, req GetTicketParamsRequest) (*TicketParams, error) {
	done := observability.StartPaymentClientCall("get_ticket_params")
	res, err := m.inner.GetTicketParams(ctx, req)
	done(resultLabel(err))
	return res, err
}

func (m *metered) OpenSession(ctx context.Context, req OpenSessionRequest) (*OpenSessionResult, error) {
	done := observability.StartPaymentClientCall("open_session")
	res, err := m.inner.OpenSession(ctx, req)
	done(resultLabel(err))
	return res, err
}

func (m *metered) ProcessPayment(ctx context.Context, req ProcessPaymentRequest) (*ProcessPaymentResult, error) {
	done := observability.StartPaymentClientCall("process_payment")
	res, err := m.inner.ProcessPayment(ctx, req)
	done(resultLabel(err))
	return res, err
}

func (m *metered) DebitBalance(ctx context.Context, req DebitBalanceRequest) (*DebitResult, error) {
	done := observability.StartPaymentClientCall("debit_balance")
	res, err := m.inner.DebitBalance(ctx, req)
	done(resultLabel(err))
	return res, err
}

func (m *metered) SufficientBalance(ctx context.Context, req SufficientBalanceRequest) (*SufficientBalanceResult, error) {
	done := observability.StartPaymentClientCall("sufficient_balance")
	res, err := m.inner.SufficientBalance(ctx, req)
	done(resultLabel(err))
	return res, err
}

func (m *metered) GetBalance(ctx context.Context, sender []byte, workID string) (*big.Int, error) {
	done := observability.StartPaymentClientCall("get_balance")
	res, err := m.inner.GetBalance(ctx, sender, workID)
	done(resultLabel(err))
	return res, err
}

func (m *metered) CloseSession(ctx context.Context, sender []byte, workID string) error {
	done := observability.StartPaymentClientCall("close_session")
	err := m.inner.CloseSession(ctx, sender, workID)
	done(resultLabel(err))
	return err
}

// Compile-time interface check.
var _ Client = (*metered)(nil)
