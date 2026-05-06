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

	// Path is the file path the fixture was loaded from; used in error
	// messages, not parsed from YAML.
	Path string `yaml:"-"`
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
