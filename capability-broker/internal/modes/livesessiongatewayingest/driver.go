package livesessiongatewayingest

import (
	"context"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/modes"
)

// Mode is the canonical mode-name@vN string for broker-owned live sessions
// where the gateway owns public ingest and the runner writes outputs to
// gateway-owned object storage.
const Mode = "live-session-gateway-ingest@v0"

// Driver exists so broker config validation can accept capabilities that
// advertise this mode. Session-open is handled by the dedicated live-runner
// broker path in internal/server/live_runner_sessions.go.
type Driver struct{}

func New() *Driver { return &Driver{} }

func (d *Driver) Mode() string { return Mode }

func (d *Driver) Serve(context.Context, modes.Params) error { return nil }
