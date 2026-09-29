package middleware

import (
	"context"
	"errors"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

var ErrJobNotAdmitted = errors.New("authorization fenced unused")

var ErrJobCapacity = errors.New("job capacity exhausted")

// JobLifecycle persists financial transitions before their external effects.
// Installed by the broker; isolated middleware tests may omit it.
type JobLifecycle interface {
	Prepare(*PendingDebit, []byte) error
	Admitted(*payment.AdmitAuthorizationResult) error
	Executing() error
	Uncertain()
	Settlement(*PendingDebit, int) error
	Complete(*pb.SettlementRecord) (string, error)
	Refused(context.Context, error) error
}
type jobLifecycleKey struct{}

func WithJobLifecycle(ctx context.Context, life JobLifecycle) context.Context {
	return context.WithValue(ctx, jobLifecycleKey{}, life)
}
func JobLifecycleFrom(ctx context.Context) JobLifecycle {
	v, _ := ctx.Value(jobLifecycleKey{}).(JobLifecycle)
	return v
}
