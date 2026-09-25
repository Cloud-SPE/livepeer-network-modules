package payment

import (
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

func TestAdmissionDispositionRequiresKnownReason(t *testing.T) {
	for _, tc := range []struct {
		reason  string
		refused bool
	}{{"INSUFFICIENT_WHOLESALE_CREDIT", true}, {"AUTHORIZATION_NOT_ACTIVE", false}, {"future_private_reason", false}, {"", false}} {
		st, _ := status.New(codes.FailedPrecondition, "private upstream details").WithDetails(&errdetails.ErrorInfo{Domain: "payments.livepeer.org", Reason: tc.reason})
		if AdmissionRefused(st.Err()) != tc.refused {
			t.Fatalf("policy for %s", tc.reason)
		}
		if tc.reason == "future_private_reason" && SafeAdmissionReason(st.Err()) != "ADMISSION_OUTCOME_UNKNOWN" {
			t.Fatal("unbounded reason exposed")
		}
	}
}
