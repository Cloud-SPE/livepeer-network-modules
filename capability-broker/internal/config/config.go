// Package config defines the broker's host-config.yaml grammar and provides
// loading + validation primitives. The grammar mirrors
// capability-broker/examples/host-config.example.yaml and is the operator's
// entire day-to-day surface.
package config

import (
	"encoding/json"
	"time"
)

// Config is the top-level host-config.yaml schema.
type Config struct {
	Identity Identity `yaml:"identity"`
	// ExternalBaseURL is the broker's externally-reachable base URL
	// (e.g. https://broker.example.com). Runner callback URLs and
	// session control URLs are derived from it — never from inbound
	// request headers. Required once any paid-session capability is
	// declared.
	ExternalBaseURL string        `yaml:"external_base_url,omitempty"`
	Listen          Listen        `yaml:"listen,omitempty"`
	AdminAuth       AuthConfig    `yaml:"admin_auth,omitempty"`
	PaymentDaemon   PaymentDaemon `yaml:"payment_daemon,omitempty"`
	SessionStore    SessionStore  `yaml:"session_store,omitempty"`
	// CredentialStore holds runner attach credentials (plan 0043 §3.3).
	// Required once runners attach with store-issued credentials; when
	// absent, connected workers authenticate with the legacy per-backend
	// worker_session_credential config string.
	CredentialStore CredentialStoreConfig `yaml:"credential_store,omitempty"`
	PoolSnapshot    PoolSnapshot          `yaml:"pool_snapshot,omitempty"`
	ReceiptSink     ReceiptSink           `yaml:"receipt_sink,omitempty"`
	// Offers is the plan-0043 operator grammar (offers.go). OffersSource
	// is "file" (default) or "admin" (pushed by pool-controller).
	Offers       []Offer `yaml:"offers,omitempty"`
	OffersSource string  `yaml:"offers_source,omitempty"`
	// OffersStatePath persists frozen shapes (and admin-pushed offers)
	// across restarts. Required in production once offers exist: a
	// frozen shape that vanished on restart would re-freeze from
	// whichever runner certified first — a silent manifest change.
	OffersStatePath string `yaml:"offers_state_path,omitempty"`
	// Capabilities is the legacy tuple grammar (backend URLs, runner
	// facts restated by the operator). Deleted once attach + freeze
	// (plan 0043 items 7–8) land.
	Capabilities []Capability `yaml:"capabilities,omitempty"`
}

// CredentialStoreConfig configures the sealed runner-credential store
// (internal/credentialstore). Path is a bbolt file on a persistent
// volume; SealingKeyFile holds the 32-byte key (raw or hex) — the same
// format as session_store.sealing_key_file, and it MAY be the same
// file. Expiry bounds apply to POST /admin/v1/enroll.
type CredentialStoreConfig struct {
	Path                 string `yaml:"path,omitempty"`
	SealingKeyFile       string `yaml:"sealing_key_file,omitempty"`
	DefaultExpirySeconds int    `yaml:"default_expiry_seconds,omitempty"` // default 90 days
	MaxExpirySeconds     int    `yaml:"max_expiry_seconds,omitempty"`     // default 365 days
}

// Enabled reports whether a store is configured.
func (c CredentialStoreConfig) Enabled() bool { return c.Path != "" }

// SessionStore configures the durable paid-session store
// (internal/sessionstore). Path is the bbolt database file — it must
// live on a persistent volume, since losing it orphans every active
// session. SealingKeyFile names a file holding the 32-byte key (raw or
// hex) that seals descriptor private parts at rest. Both are required
// once any capability declares a paid-session protocol.
type SessionStore struct {
	Path           string `yaml:"path,omitempty"`
	SealingKeyFile string `yaml:"sealing_key_file,omitempty"`
	// JobRetention is how long a terminal paid-job record is kept.
	//
	// It bounds two different things and the larger one governs. The
	// first is the idempotency window — how late a retry can still
	// converge on the recorded outcome. The second is how long the
	// broker can answer "was this exchange ever admitted", which a
	// clearinghouse needs to resolve an encumbrance held against a
	// payment envelope that may never have been used.
	//
	// The second window opens when the envelope EXPIRES, because before
	// then the envelope is still spendable and the question is
	// premature. So retention shorter than the expiry window deletes the
	// evidence before anybody is able to ask for it. Expiry is
	// creation_round + 2, which on Arbitrum's ~19h rounds is roughly
	// 38–57 hours depending on where in a round the mint landed — so a
	// 24h default, which is what this was, guaranteed the record was
	// gone first.
	//
	// Zero means the default below.
	JobRetention time.Duration `yaml:"job_retention,omitempty"`
}

// Identity carries the orch's chain identity. Must be present.
type Identity struct {
	OrchEthAddress string `yaml:"orch_eth_address"`
	Label          string `yaml:"label,omitempty"`
	// SettlementKeyFile holds the hex secp256k1 private key this broker
	// signs settlement records with. It is a HOT key the orch's cold key
	// delegates to through the manifest's settlement_keys block — never
	// the cold key itself, because a broker is network-exposed and
	// compromising one must not cost the operator its on-chain identity.
	//
	// Absent means settlement records go out unsigned. That keeps a
	// mock-payment deployment runnable; a consumer that needs integrity
	// rejects an unsigned envelope.
	SettlementKeyFile string `yaml:"settlement_key_file,omitempty"`
	// SettlementKeyNotBefore / SettlementKeyExpiresAt mirror the window
	// published for this key in the manifest's settlement_keys block
	// (RFC3339). The broker refuses to sign outside them, so it cannot
	// emit evidence that no consumer will accept. Empty means unbounded.
	SettlementKeyNotBefore string `yaml:"settlement_key_not_before,omitempty"`
	SettlementKeyExpiresAt string `yaml:"settlement_key_expires_at,omitempty"`
}

// Listen declares the broker's bind addresses. If omitted, defaults are used.
type Listen struct {
	Paid       string `yaml:"paid,omitempty"`        // default ":8080"
	Metrics    string `yaml:"metrics,omitempty"`     // default ":9090"
	WorkerQUIC string `yaml:"worker_quic,omitempty"` // optional UDP listener for connected workers
}

// PaymentDaemon describes how to reach the co-located payment-daemon. v0.1
// uses a stub client when Mock is true.
type PaymentDaemon struct {
	Socket string `yaml:"socket,omitempty"`
	Mock   bool   `yaml:"mock,omitempty"`
	// MockStatePath makes the in-process mock's ledger survive the
	// process, modelling the real daemon's durable store. Test/dev
	// surface only; ignored unless Mock is true. Without it the mock
	// is amnesiac, which is a legitimate configuration for exercising
	// the fail-closed half of session recovery.
	MockStatePath string `yaml:"mock_state_path,omitempty"`
}

// PoolSnapshot configures optional polling of pool-controller's backend
// selection snapshot surface. When omitted, the broker keeps a no-op cache and
// does not annotate /registry/health with Pool state.
type PoolSnapshot struct {
	URL            string     `yaml:"url,omitempty"`
	Auth           AuthConfig `yaml:"auth,omitempty"`
	TimeoutMS      int        `yaml:"timeout_ms,omitempty"`
	PollIntervalMS int        `yaml:"poll_interval_ms,omitempty"`
	StaleAfterMS   int        `yaml:"stale_after_ms,omitempty"`
	ExpireAfterMS  int        `yaml:"expire_after_ms,omitempty"`
}

// ReceiptSink configures optional best-effort posting of work receipts to a
// pool-controller admin API. When omitted, the broker emits no receipt events.
type ReceiptSink struct {
	URL       string     `yaml:"url,omitempty"`
	Auth      AuthConfig `yaml:"auth,omitempty"`
	TimeoutMS int        `yaml:"timeout_ms,omitempty"`
}

// Capability is one entry in the host-config.yaml capabilities array.
type Capability struct {
	ID          string         `yaml:"id"`
	OfferingID  string         `yaml:"offering_id"`
	Protocol    string         `yaml:"protocol"`
	Job         *JobCapability `yaml:"job,omitempty"`
	Session     *SessionCap    `yaml:"session,omitempty"`
	WorkUnit    WorkUnit       `yaml:"work_unit"`
	Health      Health         `yaml:"health,omitempty"`
	Price       Price          `yaml:"price"`
	Backend     Backend        `yaml:"backend"`
	Extra       map[string]any `yaml:"extra,omitempty"`
	Constraints map[string]any `yaml:"constraints,omitempty"`
}

// JobCapability carries the paid-job/v1 declared axes.
type JobCapability struct {
	// Transports is the non-empty subset of unary|stream|multipart the
	// offering serves; requests negotiate per-transport.
	Transports []string `yaml:"transports"`
}

// SessionCap carries the paid-session/v1 declared axes plus the
// broker-side backend paths (operator configuration, per A4 — no URL
// space is imposed by the protocol).
type SessionCap struct {
	DescriptorSchema string           `yaml:"descriptor_schema"`
	Heartbeat        SessionHeartbeat `yaml:"heartbeat,omitempty"`
	LeaseMaxSeconds  int              `yaml:"lease_max_seconds,omitempty"`
	// LeasePolicy is the manifest's lease.policy axis:
	// "funding-tracking" (default) derives the lease from funded runway;
	// "fixed" grants lease_max_seconds regardless of funding, for
	// offerings whose runway is managed out of band.
	LeasePolicy    string  `yaml:"lease_policy,omitempty"`
	BurnRatePerSec float64 `yaml:"burn_rate_per_second,omitempty"`
	MinRunwayUnits int64   `yaml:"min_runway_units,omitempty"`
	// MaxRotations caps how many times a session may be rebound onto a
	// rotated payment identity. 0 means the default (3). An unbounded
	// rotate-and-rebind loop would burn the payer's deposit without ever
	// delivering work.
	MaxRotations int `yaml:"max_rotations,omitempty"`
	// Attachment and Metering are advertised axes (offering-axes.md §3).
	// Defaults: external / runner-reported — the only combination this
	// broker implements today, but declared explicitly because
	// counterparties gate on them.
	Attachment string `yaml:"attachment,omitempty"`
	Metering   string `yaml:"metering,omitempty"`
	// Refill declares whether top-ups are accepted after open.
	Refill string `yaml:"refill,omitempty"`
	// ToleranceBandPct and RunwayIncrementUnits are advisory economics
	// the buyer reads at route selection; the broker never gates on them.
	ToleranceBandPct     float64            `yaml:"tolerance_band_pct,omitempty"`
	RunwayIncrementUnits int64              `yaml:"runway_increment_units,omitempty"`
	Runner               SessionRunnerPaths `yaml:"runner"`
	// SessionParamsSchema is not operator-authored: the describe pass
	// fills it from the runner's declaration so it can be advertised to
	// gateways. Never enforced by the broker.
	SessionParamsSchema json.RawMessage `yaml:"-" json:"-"`
}

// AdvertisedLeasePolicy returns the lease policy with its default.
func (s *SessionCap) AdvertisedLeasePolicy() string {
	if s.LeasePolicy == "" {
		return "funding-tracking"
	}
	return s.LeasePolicy
}

// AdvertisedAttachment returns the attachment axis with its default.
func (s *SessionCap) AdvertisedAttachment() string {
	if s.Attachment == "" {
		return "external"
	}
	return s.Attachment
}

// AdvertisedMetering returns the metering axis with its default.
func (s *SessionCap) AdvertisedMetering() string {
	if s.Metering == "" {
		return "runner-reported"
	}
	return s.Metering
}

// AdvertisedRefill returns the refill axis with its default.
func (s *SessionCap) AdvertisedRefill() string {
	if s.Refill == "" {
		return "extensible"
	}
	return s.Refill
}

// SessionHeartbeat mirrors the offering axes heartbeat object.
type SessionHeartbeat struct {
	IntervalSeconds int `yaml:"interval_seconds,omitempty"` // default 10
	MissedThreshold int `yaml:"missed_threshold,omitempty"` // default 3
}

// SessionRunnerPaths declares the runner's session API paths relative
// to backend.url; {id} is replaced with the runner session id.
type SessionRunnerPaths struct {
	CreatePath    string `yaml:"create_path"`
	StatusPath    string `yaml:"status_path"`
	TerminatePath string `yaml:"terminate_path"`
	// DescribePath is optional. When set, the broker reads the runner's
	// own declaration of what it implements at startup and on reload,
	// and refuses to serve a capability the runner contradicts
	// (paid-session/v1 §7.1.1). Advisory only — never adopted into the
	// published offering, which is cold-key signed.
	DescribePath string `yaml:"describe_path,omitempty"`
}

func (c Capability) GetBackendID() string {
	return c.Backend.ID
}

func (c Capability) GetBackendURL() string {
	return c.Backend.URL
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
	ID                      string     `yaml:"id,omitempty"`
	Transport               string     `yaml:"transport"`
	URL                     string     `yaml:"url,omitempty"`
	Auth                    AuthConfig `yaml:"auth,omitempty"`
	HostEnrollmentID        string     `yaml:"host_enrollment_id,omitempty"`
	HardwareUnitID          string     `yaml:"hardware_unit_id,omitempty"`
	GPUUUID                 string     `yaml:"gpu_uuid,omitempty"`
	TemplateID              string     `yaml:"template_id,omitempty"`
	WorkerSessionCredential string     `yaml:"worker_session_credential,omitempty"`
	MaxInFlight             int        `yaml:"max_in_flight,omitempty"`
	QueueLimit              int        `yaml:"queue_limit,omitempty"`
}
