// Package config defines the broker's host-config.yaml grammar and provides
// loading + validation primitives. The grammar mirrors
// capability-broker/examples/host-config.example.yaml and is the operator's
// entire day-to-day surface.
package config

// Config is the top-level host-config.yaml schema.
type Config struct {
	Identity      Identity      `yaml:"identity"`
	Listen        Listen        `yaml:"listen,omitempty"`
	PaymentDaemon PaymentDaemon `yaml:"payment_daemon,omitempty"`
	Capabilities  []Capability  `yaml:"capabilities"`
}

// Identity carries the orch's chain identity. Must be present.
type Identity struct {
	OrchEthAddress string `yaml:"orch_eth_address"`
	Label          string `yaml:"label,omitempty"`
}

// Listen declares the broker's bind addresses. If omitted, defaults are used.
type Listen struct {
	Paid    string `yaml:"paid,omitempty"`    // default ":8080"
	Metrics string `yaml:"metrics,omitempty"` // default ":9090"
}

// PaymentDaemon describes how to reach the co-located payment-daemon. v0.1
// uses a stub client when Mock is true.
type PaymentDaemon struct {
	Socket string `yaml:"socket,omitempty"`
	Mock   bool   `yaml:"mock,omitempty"`
}

// Capability is one entry in the host-config.yaml capabilities array.
type Capability struct {
	ID              string         `yaml:"id"`
	OfferingID      string         `yaml:"offering_id"`
	InteractionMode string         `yaml:"interaction_mode"`
	WorkUnit        WorkUnit       `yaml:"work_unit"`
	Health          Health         `yaml:"health,omitempty"`
	Price           Price          `yaml:"price"`
	Backend         Backend        `yaml:"backend"`
	Extra           map[string]any `yaml:"extra,omitempty"`
	Constraints     map[string]any `yaml:"constraints,omitempty"`
}

// Health configures per-tuple live-health behavior.
type Health struct {
	InitialStatus string      `yaml:"initial_status,omitempty"`
	Drain         HealthDrain `yaml:"drain,omitempty"`
	Probe         HealthProbe `yaml:"probe,omitempty"`
}

type HealthDrain struct {
	Enabled bool `yaml:"enabled,omitempty"`
}

// HealthProbe selects a broker-local probe recipe and cadence.
type HealthProbe struct {
	Type           string         `yaml:"type,omitempty"`
	IntervalMS     int            `yaml:"interval_ms,omitempty"`
	TimeoutMS      int            `yaml:"timeout_ms,omitempty"`
	UnhealthyAfter int            `yaml:"unhealthy_after,omitempty"`
	HealthyAfter   int            `yaml:"healthy_after,omitempty"`
	Config         map[string]any `yaml:"config,omitempty"`
}

// WorkUnit declares the metering dimension and the recipe used to compute it.
// Extractor is a type-tagged map; the broker dispatches by Extractor["type"].
type WorkUnit struct {
	Name      string         `yaml:"name"`
	Extractor map[string]any `yaml:"extractor"`
}

// Price is wei-per-unit; AmountWei is a decimal string to preserve precision
// beyond JSON's safe-integer range (per manifest schema).
type Price struct {
	AmountWei string `yaml:"amount_wei"`
	PerUnits  uint64 `yaml:"per_units"`
}

// Backend describes how the broker forwards a request to the upstream backend.
type Backend struct {
	Transport string     `yaml:"transport"`
	URL       string     `yaml:"url,omitempty"`
	Auth      AuthConfig `yaml:"auth,omitempty"`
	// Profile names the encoder preset for transport=ffmpeg-subprocess.
	// One of: passthrough | h264-live-1080p-nvenc |
	// h264-live-1080p-qsv | h264-live-1080p-vaapi |
	// h264-live-1080p-libx264.
	Profile string `yaml:"profile,omitempty"`
	// SessionRunner declares the per-session subprocess for
	// transport=session-runner (session-control-plus-media mode).
	SessionRunner *SessionRunnerBackend `yaml:"session_runner,omitempty"`
}

// SessionRunnerBackend captures the operator-supplied launch spec for
// the per-session container the broker stands up under
// transport=session-runner.
type SessionRunnerBackend struct {
	Image          string                  `yaml:"image"`
	Command        []string                `yaml:"command,omitempty"`
	Env            map[string]string       `yaml:"env,omitempty"`
	Resources      SessionRunnerResources  `yaml:"resources,omitempty"`
	StartupTimeout string                  `yaml:"startup_timeout,omitempty"`
	NetworkMode    string                  `yaml:"network_mode,omitempty"`
	Media          SessionRunnerMediaSpec  `yaml:"media,omitempty"`
}

// SessionRunnerResources expresses the memory / CPU / GPU envelope for
// the per-session container. Keys mirror docker run flags.
type SessionRunnerResources struct {
	Memory string `yaml:"memory,omitempty"`
	CPU    string `yaml:"cpu,omitempty"`
	GPUs   int    `yaml:"gpus,omitempty"`
}

// SessionRunnerMediaSpec declares the media-plane transports the
// runner expects on each leg.
type SessionRunnerMediaSpec struct {
	Publish SessionRunnerLeg `yaml:"publish,omitempty"`
	Egress  SessionRunnerLeg `yaml:"egress,omitempty"`
}

// SessionRunnerLeg is the per-direction transport label.
type SessionRunnerLeg struct {
	Transport string `yaml:"transport"`
}
