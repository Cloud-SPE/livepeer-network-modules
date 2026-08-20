// Package types holds the cross-cutting domain model: scraped broker
// offerings, the candidate manifest, and the signed-manifest envelope
// the coordinator publishes.
//
// The wire types intentionally mirror the broker's
// /registry/offerings response and the manifest schema in
// livepeer-network-protocol/manifest/schema.json. Decoding happens at
// the boundary; everything inside the coordinator works against these
// validated structs.
package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	MetadataStateOK             = "ok"
	MetadataStateDegraded       = "degraded"
	MetadataStateStale          = "stale"
	MetadataStateNeverSucceeded = "never_succeeded"
)

// BrokerOffering is one capability tuple as advertised by a broker's
// /registry/offerings endpoint. Mirrors
// capability-broker/internal/server/registry/offerings.go's wire shape
// minus the orch identity (carried separately).
type BrokerOffering struct {
	CapabilityID    string         `json:"capability_id"`
	OfferingID      string         `json:"offering_id"`
	Protocol        string         `json:"protocol"`
	Job             *JobAxes       `json:"job,omitempty"`
	Session         *SessionAxes   `json:"session,omitempty"`
	WorkUnit        WorkUnit       `json:"work_unit"`
	PricePerUnitWei string         `json:"price_per_unit_wei"`
	PerUnits        uint64         `json:"per_units,omitempty"`
	Extra           map[string]any `json:"extra,omitempty"`
	Constraints     map[string]any `json:"constraints,omitempty"`
}

// JobAxes carries the paid-job/v1 declared axes
// (livepeer-network-protocol/protocols/offering-axes.md §2, schema
// #/$defs/job_axes). Currently one key, "transports".
//
// It is deliberately an opaque object rather than a typed mirror: per
// offering-axes.md §4 the coordinator gates on nothing in here, it
// only carries the declaration from the broker into the bytes the
// cold key signs. A typed mirror would silently drop any axis a later
// spec minor adds, and the drop would be baked into the operator's
// signature. Pass-through cannot go stale.
type JobAxes map[string]any

// SessionAxes carries the paid-session/v1 declared axes
// (offering-axes.md §3, schema #/$defs/session_axes):
// descriptor_schema, metering, attachment, refill, heartbeat, lease,
// tolerance_band_pct, runway_increment_units. Opaque for the same
// reason as JobAxes.
type SessionAxes map[string]any

// Protocol tag prefixes the manifest schema pairs with a specific
// axes object. Unknown protocols are carried through unchanged — the
// coordinator refuses to guess, and the schema imposes no axes rule
// on them.
const (
	ProtocolPaidJobPrefix     = "paid-job/"
	ProtocolPaidSessionPrefix = "paid-session/"
)

// protocolTagRE mirrors the schema's protocol pattern
// (^[a-z][a-z0-9-]*/v[0-9]+$).
var protocolTagRE = regexp.MustCompile(`^[a-z][a-z0-9-]*/v[0-9]+$`)

// ValidateProtocolAxes enforces the manifest schema's conditional
// pairing: a paid-job/* protocol requires the job object and forbids
// session; a paid-session/* protocol requires session and forbids
// job. Both the scrape boundary and the signed-manifest receive path
// run it — a tuple that fails here produces a manifest that fails
// schema validation at every resolver, so it must never reach the
// cold key.
func ValidateProtocolAxes(protocol string, job *JobAxes, session *SessionAxes) error {
	switch {
	case strings.HasPrefix(protocol, ProtocolPaidJobPrefix):
		if job == nil {
			return fmt.Errorf("job: required for %q protocols", ProtocolPaidJobPrefix+"*")
		}
		if session != nil {
			return fmt.Errorf("session: must not be set for %q protocols", ProtocolPaidJobPrefix+"*")
		}
	case strings.HasPrefix(protocol, ProtocolPaidSessionPrefix):
		if session == nil {
			return fmt.Errorf("session: required for %q protocols", ProtocolPaidSessionPrefix+"*")
		}
		if job != nil {
			return fmt.Errorf("job: must not be set for %q protocols", ProtocolPaidSessionPrefix+"*")
		}
	}
	return nil
}

// WorkUnit is the metering dimension. Free-form name; opaque to the
// coordinator but signed into the manifest verbatim.
type WorkUnit struct {
	Name string `json:"name"`
}

// BrokerOfferings is the full /registry/offerings response.
type BrokerOfferings struct {
	OrchEthAddress string           `json:"orch_eth_address"`
	Capabilities   []BrokerOffering `json:"capabilities"`
}

// BrokerHealthCapability is one tuple-health entry from
// capability-broker /registry/health.
type BrokerHealthCapability struct {
	ID                   string                `json:"id"`
	OfferingID           string                `json:"offering_id"`
	Status               string                `json:"status"`
	Reason               string                `json:"reason,omitempty"`
	ProbeType            string                `json:"probe_type,omitempty"`
	ProbedAt             time.Time             `json:"probed_at,omitempty"`
	StaleAfter           time.Time             `json:"stale_after,omitempty"`
	ConsecutiveSuccesses int                   `json:"consecutive_successes,omitempty"`
	ConsecutiveFailures  int                   `json:"consecutive_failures,omitempty"`
	Backends             []BrokerHealthBackend `json:"backends,omitempty"`
	Metadata             *BrokerHealthMetadata `json:"metadata,omitempty"`
}

type BrokerHealthBackend struct {
	BackendID            string    `json:"backend_id,omitempty"`
	Status               string    `json:"status"`
	Reason               string    `json:"reason,omitempty"`
	ProbeType            string    `json:"probe_type,omitempty"`
	ProbedAt             time.Time `json:"probed_at,omitempty"`
	StaleAfter           time.Time `json:"stale_after,omitempty"`
	ConsecutiveSuccesses int       `json:"consecutive_successes,omitempty"`
	ConsecutiveFailures  int       `json:"consecutive_failures,omitempty"`
	SelectionEligible    bool      `json:"selection_eligible"`
	SelectionWeight      int       `json:"selection_weight,omitempty"`
	SelectionReason      string    `json:"selection_reason,omitempty"`
}

type BrokerHealthMetadata struct {
	Provider              string    `json:"provider,omitempty"`
	Applicable            bool      `json:"applicable"`
	LastAttemptAt         time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt         time.Time `json:"last_success_at,omitempty"`
	LastSuccessAgeSeconds float64   `json:"last_success_age_seconds,omitempty"`
	LastError             string    `json:"last_error,omitempty"`
	LastResult            string    `json:"last_result,omitempty"`
	ConsecutiveFailures   int       `json:"consecutive_failures,omitempty"`
}

// BrokerHealth is the full /registry/health response.
type BrokerHealth struct {
	BrokerStatus string                   `json:"broker_status"`
	GeneratedAt  time.Time                `json:"generated_at"`
	Capabilities []BrokerHealthCapability `json:"capabilities"`
}

// Validate runs a boundary-decoder pass on the freshly-scraped
// payload: orch identity match, required fields, decimal-string price,
// well-formed protocol tag paired with its declared-axes object, and
// non-empty work_unit.
func (b *BrokerOfferings) Validate(expectedOrch string) error {
	if !strings.EqualFold(strings.TrimSpace(b.OrchEthAddress), strings.TrimSpace(expectedOrch)) {
		return fmt.Errorf("orch identity mismatch: got %q, want %q", b.OrchEthAddress, expectedOrch)
	}
	for i, c := range b.Capabilities {
		if c.CapabilityID == "" {
			return fmt.Errorf("capabilities[%d].capability_id: required", i)
		}
		if c.OfferingID == "" {
			return fmt.Errorf("capabilities[%d].offering_id: required", i)
		}
		if c.Protocol == "" {
			return fmt.Errorf("capabilities[%d].protocol: required", i)
		}
		if !protocolTagRE.MatchString(c.Protocol) {
			return fmt.Errorf("capabilities[%d].protocol: must match <name>/v<major>, got %q", i, c.Protocol)
		}
		if err := ValidateProtocolAxes(c.Protocol, c.Job, c.Session); err != nil {
			return fmt.Errorf("capabilities[%d].%w", i, err)
		}
		if c.WorkUnit.Name == "" {
			return fmt.Errorf("capabilities[%d].work_unit.name: required", i)
		}
		if !isNonNegativeDecimalString(c.PricePerUnitWei) {
			return fmt.Errorf("capabilities[%d].price_per_unit_wei: must be a non-negative decimal string, got %q", i, c.PricePerUnitWei)
		}
	}
	return nil
}

// Validate checks the broker /registry/health response shape. It is a
// live unsigned signal, so validation is structural only.
func (b *BrokerHealth) Validate() error {
	for i, c := range b.Capabilities {
		if c.ID == "" {
			return fmt.Errorf("capabilities[%d].id: required", i)
		}
		if c.OfferingID == "" {
			return fmt.Errorf("capabilities[%d].offering_id: required", i)
		}
		switch c.Status {
		case "ready", "draining", "degraded", "unreachable", "stale":
		default:
			return fmt.Errorf("capabilities[%d].status: invalid %q", i, c.Status)
		}
	}
	return nil
}

func MetadataResultHealthy(result string) bool {
	switch result {
	case "enriched", "empty", "already_configured":
		return true
	default:
		return false
	}
}

func ClassifyBrokerHealthMetadata(meta *BrokerHealthMetadata, warningAfter, staleAfter time.Duration) (string, float64) {
	if meta == nil || !meta.Applicable {
		return "", -1
	}
	ageSeconds := meta.LastSuccessAgeSeconds
	if ageSeconds < 0 || meta.LastSuccessAt.IsZero() {
		return MetadataStateNeverSucceeded, -1
	}
	age := time.Duration(ageSeconds * float64(time.Second))
	if staleAfter > 0 && age >= staleAfter {
		return MetadataStateStale, ageSeconds
	}
	if meta.ConsecutiveFailures > 0 || !MetadataResultHealthy(meta.LastResult) {
		return MetadataStateDegraded, ageSeconds
	}
	if warningAfter > 0 && age >= warningAfter {
		return MetadataStateDegraded, ageSeconds
	}
	return MetadataStateOK, ageSeconds
}

// SourceTuple is one offering tagged with the broker that advertised
// it. The scrape cache holds a flat list of these; the candidate
// service deduplicates by uniqueness key.
type SourceTuple struct {
	BrokerName string
	BaseURL    string
	WorkerURL  string
	Offering   BrokerOffering
	ScrapedAt  time.Time
}

// CapabilityTuple is the manifest tuple as the coordinator emits it.
// Mirrors livepeer-network-protocol/manifest/schema.json #/$defs/capability.
type CapabilityTuple struct {
	CapabilityID string `json:"capability_id"`
	OfferingID   string `json:"offering_id"`
	// Protocol is the protocol tag ("paid-job/v1", "paid-session/v1").
	// Manifest spec 1.0.0 replaced the pre-v1 mode field with this plus
	// the declared axes below.
	Protocol string `json:"protocol"`
	// Job / Session are the declared-axes objects. Exactly one is set,
	// selected by Protocol; both nil only for an unknown protocol the
	// schema imposes no rule on. Pointers so absence survives the
	// round-trip — an emitted-but-empty object would break the
	// schema's conditional requirements.
	Job             *JobAxes     `json:"job,omitempty"`
	Session         *SessionAxes `json:"session,omitempty"`
	WorkUnit        WorkUnit     `json:"work_unit"`
	PricePerUnitWei string       `json:"price_per_unit_wei"`
	// PerUnits is the price denominator: PricePerUnitWei buys this many
	// work units. Absent (0) means 1. Carried verbatim from the broker's
	// offering into the signed bytes — dropping it here is what let a
	// per_units offering publish a quote at per_units times the rate its
	// own ledger charges.
	PerUnits    uint64         `json:"per_units,omitempty"`
	WorkerURL   string         `json:"worker_url"`
	Extra       map[string]any `json:"extra,omitempty"`
	Constraints map[string]any `json:"constraints,omitempty"`
}

// Orch is the orchestrator-identity sub-struct. Mirrors
// livepeer-network-protocol/manifest/schema.json #/$defs/orch.
type Orch struct {
	EthAddress string `json:"eth_address"`
	ServiceURI string `json:"service_uri,omitempty"`
}

// ManifestPayload is the inner manifest content (signed bytes are JCS
// over this). Mirrors livepeer-network-protocol/manifest/schema.json
// #/$defs/manifest.
type ManifestPayload struct {
	SpecVersion    string    `json:"spec_version"`
	PublicationSeq uint64    `json:"publication_seq"`
	IssuedAt       time.Time `json:"issued_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	Orch           Orch      `json:"orch"`
	// SettlementKeys are hot keys the cold key delegates settlement
	// signing to. They ride inside the signed payload because that is
	// the whole point: the cold key's signature is what makes a
	// broker-held key trustworthy to a consumer.
	SettlementKeys []SettlementKey   `json:"settlement_keys,omitempty"`
	Capabilities   []CapabilityTuple `json:"capabilities"`
}

// SettlementKey is one delegated settlement-signing key and its window.
type SettlementKey struct {
	PublicKey string    `json:"public_key"`
	NotBefore time.Time `json:"not_before"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Signature is the cold-key signature over the JCS bytes of
// ManifestPayload.
type Signature struct {
	Algorithm        string `json:"algorithm"`
	Value            string `json:"value"`
	Canonicalization string `json:"canonicalization,omitempty"`
}

// SignedManifest is the published wrapper. The receive service decodes
// uploads into this shape, verifies, and writes the bytes to the
// published store.
type SignedManifest struct {
	Manifest  ManifestPayload `json:"manifest"`
	Signature Signature       `json:"signature"`
}

// Candidate is what the coordinator hands the operator: the bytes the
// cold key will sign, plus the operator-only sidecar.
type Candidate struct {
	ManifestBytes []byte
	Manifest      ManifestPayload
	Metadata      Metadata
	// ContentHash is a stable digest over the content-bearing fields of
	// Manifest (orch + capabilities), excluding issued_at, expires_at,
	// and publication_seq. The Builder uses it to debounce issued_at so
	// the signed bytes stay identical across scrapes when no content
	// has changed. Internal only; never marshaled.
	ContentHash string `json:"-"`
}

// Metadata is the operator-only sidecar (NOT signed).
type Metadata struct {
	CandidateTimestamp              time.Time              `json:"candidate_timestamp"`
	ScrapeWindowStart               time.Time              `json:"scrape_window_start"`
	ScrapeWindowEnd                 time.Time              `json:"scrape_window_end"`
	SourceBrokers                   []MetadataBrokerEntry  `json:"source_brokers"`
	MetadataWarningThresholdSeconds int64                  `json:"metadata_warning_threshold_seconds,omitempty"`
	MetadataStaleThresholdSeconds   int64                  `json:"metadata_stale_threshold_seconds,omitempty"`
	Warnings                        []MetadataWarning      `json:"warnings,omitempty"`
	TupleMetadataWarnings           []TupleMetadataWarning `json:"tuple_metadata_warnings,omitempty"`
	CoordinatorCommit               string                 `json:"coordinator_commit"`
	SchemaVersion                   string                 `json:"schema_version"`
	HAEndpoints                     []HAEndpoint           `json:"ha_endpoints,omitempty"`
}

// MetadataBrokerEntry records per-broker scrape success/failure.
type MetadataBrokerEntry struct {
	Name                     string    `json:"name"`
	BaseURL                  string    `json:"base_url"`
	Status                   string    `json:"status"`
	ScrapedAt                time.Time `json:"scraped_at,omitempty"`
	Error                    string    `json:"error,omitempty"`
	MetadataApplicableTuples int       `json:"metadata_applicable_tuples,omitempty"`
	MetadataUnhealthyTuples  int       `json:"metadata_unhealthy_tuples,omitempty"`
	MetadataStaleTuples      int       `json:"metadata_stale_tuples,omitempty"`
	MetadataWorstAgeSeconds  float64   `json:"metadata_worst_age_seconds,omitempty"`
}

type MetadataWarning struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type TupleMetadataWarning struct {
	Code                  string  `json:"code"`
	Severity              string  `json:"severity"`
	BrokerName            string  `json:"broker_name"`
	BaseURL               string  `json:"base_url"`
	CapabilityID          string  `json:"capability_id"`
	OfferingID            string  `json:"offering_id"`
	WorkerURL             string  `json:"worker_url,omitempty"`
	MetadataState         string  `json:"metadata_state"`
	MetadataResult        string  `json:"metadata_result,omitempty"`
	MetadataError         string  `json:"metadata_error,omitempty"`
	LastSuccessAgeSeconds float64 `json:"last_success_age_seconds,omitempty"`
	ConsecutiveFailures   int     `json:"consecutive_failures,omitempty"`
}

// HAEndpoint records the alternate worker_url(s) that were dropped
// because the same uniqueness key + price was advertised by multiple
// brokers. The signed bytes carry the lex-min worker_url; the others
// surface here for operator visibility.
type HAEndpoint struct {
	CapabilityID string `json:"capability_id"`
	OfferingID   string `json:"offering_id"`
	PrimaryURL   string `json:"primary_url"`
	AlternateURL string `json:"alternate_url"`
	BrokerName   string `json:"broker_name"`
}

// ParseSignedManifest decodes bytes into a SignedManifest with strict
// JSON decoding (unknown fields rejected at the wrapper). Used by the
// receive service.
func ParseSignedManifest(b []byte) (*SignedManifest, error) {
	if len(b) == 0 {
		return nil, errors.New("empty body")
	}
	var sm SignedManifest
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sm); err != nil {
		return nil, fmt.Errorf("decode signed manifest: %w", err)
	}
	if dec.More() {
		return nil, errors.New("trailing data after signed manifest")
	}
	return &sm, nil
}

func isNonNegativeDecimalString(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
