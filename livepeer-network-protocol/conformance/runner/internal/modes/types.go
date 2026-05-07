// Package modes defines the runner's mode-driver interface and registry.
//
// Each driver implements one interaction mode (http-reqresp@v0,
// http-stream@v0, etc.) for one target role: broker (the runner is the
// gateway, hitting the broker under test) or gateway (the runner is the
// customer, hitting the gateway under test, with the in-process
// mockbackend playing the upstream broker).
//
// The runner's main loop dispatches each fixture to the driver
// matching `(target, fixture.Mode)`.
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
	Run(ctx context.Context, targetURL string, fx fixtures.Fixture, mock *mockbackend.Server) report.Result
}

// Target identifies which side of the wire the driver assumes is under
// test. Broker drivers send paid requests to the target as if they were
// a gateway; gateway drivers send unpaid customer requests to the target
// and let the mockbackend (running as a stand-in broker) verify the
// gateway-adapter middleware completes the wire shape correctly.
type Target string

const (
	TargetBroker  Target = "broker"
	TargetGateway Target = "gateway"
)

var (
	brokerDrivers  = map[string]Driver{}
	gatewayDrivers = map[string]Driver{}
)

// Register adds a broker-target driver. Convenience alias for
// RegisterFor(TargetBroker, d).
func Register(d Driver) { brokerDrivers[d.Mode()] = d }

// RegisterFor adds a driver under a given target.
func RegisterFor(t Target, d Driver) {
	switch t {
	case TargetGateway:
		gatewayDrivers[d.Mode()] = d
	default:
		brokerDrivers[d.Mode()] = d
	}
}

// Get returns the broker-target driver for the given mode string.
// Convenience alias for GetFor(TargetBroker, mode).
func Get(mode string) (Driver, bool) {
	d, ok := brokerDrivers[mode]
	return d, ok
}

// GetFor returns the driver for `(target, mode)`.
func GetFor(t Target, mode string) (Driver, bool) {
	switch t {
	case TargetGateway:
		d, ok := gatewayDrivers[mode]
		return d, ok
	default:
		d, ok := brokerDrivers[mode]
		return d, ok
	}
}

// Names returns the registered broker-target mode strings (unordered).
func Names() []string {
	out := make([]string, 0, len(brokerDrivers))
	for k := range brokerDrivers {
		out = append(out, k)
	}
	return out
}

// NamesFor returns the registered mode strings under a given target
// (unordered).
func NamesFor(t Target) []string {
	src := brokerDrivers
	if t == TargetGateway {
		src = gatewayDrivers
	}
	out := make([]string, 0, len(src))
	for k := range src {
		out = append(out, k)
	}
	return out
}
