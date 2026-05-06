// Package modes defines the runner's mode-driver interface and registry.
//
// Each driver implements one interaction mode (http-reqresp@v0,
// http-stream@v0, etc.). The runner's main loop dispatches each fixture to
// the driver matching the fixture's mode field.
package modes

import (
	"context"

	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/fixtures"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/mockbackend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/report"
)

// Driver runs a fixture against a target and returns a Result.
type Driver interface {
	Mode() string
	Run(ctx context.Context, brokerURL string, fx fixtures.Fixture, mock *mockbackend.Server) report.Result
}

var registered = map[string]Driver{}

// Register adds a driver. The key is the driver's Mode() value.
func Register(d Driver) { registered[d.Mode()] = d }

// Get returns the driver for the given mode string, or nil + false if not
// registered.
func Get(mode string) (Driver, bool) {
	d, ok := registered[mode]
	return d, ok
}

// Names returns the registered mode strings (unordered).
func Names() []string {
	out := make([]string, 0, len(registered))
	for k := range registered {
		out = append(out, k)
	}
	return out
}
