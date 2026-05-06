package middleware

import (
	"net/http"
	"strconv"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/payment"
)

// estimatedUnitsV0 is the per-request work-unit estimate the broker debits
// up front in v0.1. The real value comes from the Livepeer-Payment envelope's
// expected_max_units field; v0.1 mock-only payment uses a fixed value
// pending plan 0005 (real payment-daemon integration).
const estimatedUnitsV0 uint64 = 1

// Payment is the payment-lifecycle middleware:
//
//	OpenSession → Debit(estimate) → handler.Serve → Reconcile(actual) → Close
//
// In v0.1 mock mode it accepts any non-empty Livepeer-Payment header and
// records the lifecycle calls in the payment.Mock client (inspectable from
// tests). The middleware reads the Livepeer-Work-Units response header set
// by the mode driver to determine actual units for reconciliation.
func Payment(client payment.Client) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paymentBlob := r.Header.Get(livepeerheader.Payment)
			// Headers middleware already rejects missing Livepeer-Payment, but
			// we defend in case Payment is wired without Headers in front of it.
			if paymentBlob == "" {
				livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid,
					"missing Livepeer-Payment header")
				return
			}

			ctx := r.Context()
			session, err := client.OpenSession(ctx, payment.OpenSessionRequest{
				CapabilityID: r.Header.Get(livepeerheader.Capability),
				OfferingID:   r.Header.Get(livepeerheader.Offering),
				PaymentBlob:  paymentBlob,
			})
			if err != nil {
				livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid,
					"open session: "+err.Error())
				return
			}
			// Always close, even on early-return paths below.
			defer func() { _ = client.Close(ctx, session.ID) }()

			if err := client.Debit(ctx, session.ID, estimatedUnitsV0); err != nil {
				livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid,
					"debit estimate: "+err.Error())
				return
			}

			rec := &responseRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			// Read actual work units. The recorder snaps the value at
			// WriteHeader for non-streaming modes; for http-stream@v0 (and
			// future trailer-based modes) the value is set on the response
			// header map AFTER the body and only ends up there post-handler,
			// so we re-read as a fallback.
			actual := rec.workUnits
			if actual == 0 {
				if h := rec.Header().Get(livepeerheader.WorkUnits); h != "" {
					if n, err := strconv.ParseUint(h, 10, 64); err == nil {
						actual = n
					}
				}
			}
			if actual != estimatedUnitsV0 {
				_ = client.Reconcile(ctx, session.ID, actual)
			}
		})
	}
}
