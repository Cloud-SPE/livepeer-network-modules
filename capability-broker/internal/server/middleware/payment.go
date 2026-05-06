package middleware

import "net/http"

// Payment is the payment-lifecycle middleware: opens a session, debits the
// estimate up front, lets the inner handler Serve, and reconciles + closes
// after.
//
// v0.1 scaffold: payment client is not yet wired (plan 0003 dispatch commit
// adds internal/payment/mock.go and the gRPC client). This middleware
// currently passes through unchanged; real validation comes online in the
// next commit.
func Payment(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO(plan 0003 dispatch commit):
		//   1. Decode Livepeer-Payment envelope (base64 protobuf).
		//   2. Cross-check envelope.capability_id == r.Header(Livepeer-Capability)
		//      and envelope.offering_id == r.Header(Livepeer-Offering); reject
		//      with 401 + payment_envelope_mismatch on mismatch.
		//   3. PaymentClient.OpenSession + ProcessPayment.
		//   4. PaymentClient.DebitBalance(envelope.expected_max_units).
		//   5. Call next.ServeHTTP wrapped in a ResponseRecorder so we can read
		//      Livepeer-Work-Units before the body is committed.
		//   6. PaymentClient.Reconcile(actualUnits) + CloseSession.
		next.ServeHTTP(w, r)
	})
}
