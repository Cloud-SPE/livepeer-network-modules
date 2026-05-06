package middleware

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/payment"
)

// Payment is the payment-lifecycle middleware:
//
//	decode envelope → cross-check → OpenSession → Debit(estimate)
//	  → handler.Serve → Reconcile(actual) → Close
//
// The estimate is the envelope's expected_max_units (no longer hard-coded).
// The middleware reads the Livepeer-Work-Units response header set by the
// mode driver to determine actual units for reconciliation.
//
// Decoding/cross-check failures map to:
//   - DecodeError      → HTTP 401 + Livepeer-Error: payment_invalid
//   - MismatchError    → HTTP 401 + Livepeer-Error: payment_envelope_mismatch
//   - any other error  → HTTP 401 + Livepeer-Error: payment_invalid
func Payment(client payment.Client) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paymentBlob := r.Header.Get(livepeerheader.Payment)
			// Headers middleware already rejects missing Livepeer-Payment, but
			// we defend in case Payment is wired without Headers in front.
			if paymentBlob == "" {
				livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid,
					"missing Livepeer-Payment header")
				return
			}

			capability := r.Header.Get(livepeerheader.Capability)
			offering := r.Header.Get(livepeerheader.Offering)

			env, err := payment.DecodeEnvelope(paymentBlob)
			if err != nil {
				livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid,
					err.Error())
				return
			}
			if err := payment.CrossCheck(env, capability, offering); err != nil {
				livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentEnvelopeMismatch,
					err.Error())
				return
			}

			ctx := r.Context()
			session, err := client.OpenSession(ctx, payment.OpenSessionRequest{
				CapabilityID:   capability,
				OfferingID:     offering,
				PaymentBlob:    paymentBlob,
				DecodedPayment: env,
			})
			if err != nil {
				code, errCode := mapClientErr(err)
				livepeerheader.WriteError(w, code, errCode, "open session: "+err.Error())
				return
			}
			// Always close, even on early-return paths below.
			defer func() { _ = client.Close(ctx, session.ID) }()

			estimate := env.GetExpectedMaxUnits()
			if err := client.Debit(ctx, session.ID, estimate); err != nil {
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
			if actual != estimate {
				_ = client.Reconcile(ctx, session.ID, actual)
			}
		})
	}
}

// mapClientErr distinguishes mismatch / decode failures returned from the
// payment-daemon (defense-in-depth: the daemon also performs its own
// validation and may surface InvalidArgument). Everything else collapses to
// payment_invalid.
func mapClientErr(err error) (int, string) {
	var mErr *payment.MismatchError
	if errors.As(err, &mErr) {
		return http.StatusUnauthorized, livepeerheader.ErrPaymentEnvelopeMismatch
	}
	return http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid
}
