// Package receiver implements the PayeeDaemon RPC surface — validates
// incoming payment blobs, tracks per-(sender, work_id) balances, and
// (post chain integration) redeems winning tickets via the
// TicketBroker.
//
// v0.2 status: SCAFFOLD. All RPCs return UNIMPLEMENTED. Plan 0014 C4
// fills in OpenSession / ProcessPayment / DebitBalance / CloseSession
// against the new RPC surface; the broker stays on the v0.1 surface
// until that commit lands.
package receiver

import (
	"log/slog"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// Service implements pb.PayeeDaemonServer.
type Service struct {
	pb.UnimplementedPayeeDaemonServer

	logger *slog.Logger
}

// New constructs a receiver Service. v0.2 takes no providers; C4 wires
// store / clock / broker / keystore.
func New(logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{logger: logger}
}
