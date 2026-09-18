package middleware

import (
	"bytes"
	"context"
)

type admissionFailureKey struct{}

// AdmissionFailure contains a definitive receiver refusal, never a timeout or
// an inferred status. The receiver verified this authorization before reporting
// an insufficient account balance. No workload handler was entered.
type AdmissionFailure struct{ Authorization []byte }

func WithAdmissionFailure(ctx context.Context) (context.Context, *AdmissionFailure) {
	slot := &AdmissionFailure{}
	return context.WithValue(ctx, admissionFailureKey{}, slot), slot
}
func recordAdmissionFailure(ctx context.Context, authorization []byte) {
	if slot, ok := ctx.Value(admissionFailureKey{}).(*AdmissionFailure); ok {
		slot.Authorization = bytes.Clone(authorization)
	}
}
