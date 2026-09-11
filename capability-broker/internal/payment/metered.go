package payment

import (
	"context"
	"errors"

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

func (m *metered) accountClient() (AccountClient, error) {
	a, ok := m.inner.(AccountClient)
	if !ok {
		return nil, errors.ErrUnsupported
	}
	return a, nil
}

func (m *metered) AdmitAuthorization(ctx context.Context, req AdmitAuthorizationRequest) (*AdmitAuthorizationResult, error) {
	done := observability.StartPaymentClientCall("admit_authorization")
	a, err := m.accountClient()
	if err != nil {
		done(resultLabel(err))
		return nil, err
	}
	res, err := a.AdmitAuthorization(ctx, req)
	done(resultLabel(err))
	return res, err
}

func (m *metered) FundWholesaleAccount(ctx context.Context, paymentBytes []byte) (*FundWholesaleAccountResult, error) {
	done := observability.StartPaymentClientCall("fund_wholesale_account")
	a, err := m.accountClient()
	if err != nil {
		done(resultLabel(err))
		return nil, err
	}
	res, err := a.FundWholesaleAccount(ctx, paymentBytes)
	done(resultLabel(err))
	return res, err
}

func (m *metered) AdvanceAuthorization(ctx context.Context, req AdvanceAuthorizationRequest) (*AdvanceAuthorizationResult, error) {
	done := observability.StartPaymentClientCall("advance_authorization")
	a, err := m.accountClient()
	if err != nil {
		done(resultLabel(err))
		return nil, err
	}
	res, err := a.AdvanceAuthorization(ctx, req)
	done(resultLabel(err))
	return res, err
}

func (m *metered) SettleAuthorization(ctx context.Context, req SettleAuthorizationRequest) (*SettleAuthorizationResult, error) {
	done := observability.StartPaymentClientCall("settle_authorization")
	a, err := m.accountClient()
	if err != nil {
		done(resultLabel(err))
		return nil, err
	}
	res, err := a.SettleAuthorization(ctx, req)
	done(resultLabel(err))
	return res, err
}

func (m *metered) GetWholesaleAccount(ctx context.Context, payer []byte) (*WholesaleAccount, error) {
	done := observability.StartPaymentClientCall("get_wholesale_account")
	a, err := m.accountClient()
	if err != nil {
		done(resultLabel(err))
		return nil, err
	}
	res, err := a.GetWholesaleAccount(ctx, payer)
	done(resultLabel(err))
	return res, err
}

func (m *metered) GetSpendAuthorization(ctx context.Context, payer []byte, authorizationID string) (*SpendAuthorizationStatus, error) {
	done := observability.StartPaymentClientCall("get_spend_authorization")
	a, err := m.accountClient()
	if err != nil {
		done(resultLabel(err))
		return nil, err
	}
	res, err := a.GetSpendAuthorization(ctx, payer, authorizationID)
	done(resultLabel(err))
	return res, err
}

// Compile-time interface check.
var _ Client = (*metered)(nil)
var _ AccountClient = (*metered)(nil)
