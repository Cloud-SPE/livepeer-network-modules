// Package fixtures defines the YAML fixture format the runner consumes and
// the loader that walks fixture folders.
//
// The format mirrors livepeer-network-protocol/conformance/fixtures/README.md
// and the worked example at
// livepeer-network-protocol/conformance/fixtures/http-reqresp/happy-path.yaml.
//
// v0.1 status: provisional. Field shapes may evolve as more modes get
// drivers (plan 0006).
package fixtures

// Fixture is one scenario the runner executes against a target. The mode
// field selects which driver runs the fixture.
type Fixture struct {
	Name              string            `yaml:"name"`
	Mode              string            `yaml:"mode"`
	SpecVersion       string            `yaml:"spec_version"`
	Description       string            `yaml:"description,omitempty"`
	Setup             Setup             `yaml:"setup"`
	Request           Request           `yaml:"request"`
	BackendResponse   BackendResponse   `yaml:"backend_response"`
	BackendAssertions BackendAssertions `yaml:"backend_assertions,omitempty"`
	ResponseExpect    ResponseExpect    `yaml:"response_expect"`

	// WSRealtime carries ws-realtime-mode-specific scenario knobs (plan
	// 0015 conformance fixtures). Empty for fixtures of other modes.
	WSRealtime WSRealtimeFixture `yaml:"ws_realtime,omitempty"`

	// Path is the file path the fixture was loaded from; used in error
	// messages, not parsed from YAML.
	Path string `yaml:"-"`
}

// WSRealtimeFixture extends Fixture with the ws-realtime-driver knobs
// plan 0015's interim-debit fixtures need (frame schedule + assertions).
// Zero values reproduce the v0.1 single-frame happy path.
type WSRealtimeFixture struct {
	// FrameCount is the number of frames the runner sends. 0 → 1 frame
	// (the v0.1 single-message round-trip).
	FrameCount int `yaml:"frame_count,omitempty"`
	// FrameIntervalMs is the gap between frames. 0 → send all frames
	// immediately.
	FrameIntervalMs int `yaml:"frame_interval_ms,omitempty"`
	// FrameSizeBytes is the per-frame payload size. 0 → small probe.
	FrameSizeBytes int `yaml:"frame_size_bytes,omitempty"`
	// HoldAfterFramesMs is how long the runner holds the connection
	// open after sending the last frame. Used when the runner expects
	// the broker to terminate the session (balance-exhausted) so the
	// runner observes the close from the server side.
	HoldAfterFramesMs int `yaml:"hold_after_frames_ms,omitempty"`

	// ExpectMinInterimDebits asserts the daemon issued at least this
	// many DebitBalance calls during the session. Inferred via the
	// payer-daemon's GetBalance trajectory at the runner side
	// (indirect inference per plan 0015 §9.1).
	ExpectMinInterimDebits int `yaml:"expect_min_interim_debits,omitempty"`
	// ExpectBrokerTerminated asserts the broker closed the WebSocket,
	// not the runner. Used by the balance-exhausted fixture.
	ExpectBrokerTerminated bool `yaml:"expect_broker_terminated,omitempty"`
}

// Setup describes what the runner is expected to provision in the broker
// under test. v0.1 trusts the operator to have configured the broker
// in a matching way (see conformance/test-broker-config.yaml). Future
// versions may have the runner orchestrate broker startup itself.
type Setup struct {
	Capability SetupCapability `yaml:"capability"`
}

// SetupCapability mirrors the broker's host-config.yaml capability entry.
// The runner uses this to translate fixtures into the broker config that
// satisfies them.
type SetupCapability struct {
	ID              string         `yaml:"id"`
	OfferingID      string         `yaml:"offering_id"`
	InteractionMode string         `yaml:"interaction_mode"`
	WorkUnit        SetupWorkUnit  `yaml:"work_unit"`
	Price           SetupPrice     `yaml:"price"`
	Backend         SetupBackend   `yaml:"backend"`
	Extra           map[string]any `yaml:"extra,omitempty"`
}

type SetupWorkUnit struct {
	Name      string         `yaml:"name"`
	Extractor map[string]any `yaml:"extractor"`
}

type SetupPrice struct {
	AmountWei string `yaml:"amount_wei"`
	PerUnits  uint64 `yaml:"per_units"`
}

type SetupBackend struct {
	Transport string `yaml:"transport"`
	URL       string `yaml:"url,omitempty"`
	Auth      any    `yaml:"auth,omitempty"`
}

// Request is what the runner sends to the broker.
type Request struct {
	Method  string            `yaml:"method"`
	Path    string            `yaml:"path"`
	Headers map[string]string `yaml:"headers"`
	Body    string            `yaml:"body"`
}

// BackendResponse is what the runner-provided mock backend returns when the
// broker forwards.
type BackendResponse struct {
	Status  int               `yaml:"status"`
	Headers map[string]string `yaml:"headers"`
	Body    string            `yaml:"body"`
}

// BackendAssertions is what the runner asserts about the call the broker
// made to the mock backend (side-effect verification).
type BackendAssertions struct {
	Method                     string `yaml:"method,omitempty"`
	BodyReceivedRaw            string `yaml:"body_received_raw,omitempty"`
	LivepeerHeadersPresent     *bool  `yaml:"livepeer_headers_present,omitempty"`
	AuthorizationHeaderPresent *bool  `yaml:"authorization_header_present,omitempty"`
}

// ResponseExpect is what the runner asserts about the response the broker
// sent back to the runner.
type ResponseExpect struct {
	Status            int      `yaml:"status"`
	HeadersPresent    []string `yaml:"headers_present,omitempty"`
	LivepeerWorkUnits *uint64  `yaml:"livepeer_work_units,omitempty"`
	BodyPassthrough   bool     `yaml:"body_passthrough,omitempty"`

	// BodyFieldsPresent is a list of JSONPath-ish dotted paths that MUST
	// resolve to non-empty values in the response body (which MUST be
	// JSON). Used by session-open fixtures (rtmp-ingress-hls-egress and
	// session-control-plus-media) to assert dynamic URL fields without
	// pinning their exact values.
	BodyFieldsPresent []string `yaml:"body_fields_present,omitempty"`
}
