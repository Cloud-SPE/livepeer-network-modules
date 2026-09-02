package config

import (
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
)

// Offers — the operator's side of plan 0043 (§3.1).
//
// An offer is what is sold: an offering id under a capability, at a price,
// with a capacity, in a place (extra), gated by certification. Everything
// the runner knows about itself — transports, descriptor schemas, work
// unit, extractor, paths, readiness, model identity — is NOT here. It
// arrives in the runner's attach document (protocols/runner-attach.md)
// and is frozen into the offer by the first certified runner
// (protocols/broker-admin.md §4). `match` selects which attached runners
// an offer wants by their declared identity.
//
// `offers[]` is the whole of the operator's declaration. There is no
// second grammar: the legacy `capabilities[]` tuples, with their backend
// URLs and hand-copied runner facts, were deleted once attach and freeze
// landed (plan 0043 items 7–8, `lnm-sk7`).

// OffersSourceFile means offers[] comes from this file; OffersSourceAdmin
// means a pool-controller pushes them over PUT /admin/v1/offers
// (broker-admin §4.2) and the file's offers[] must be empty.
const (
	OffersSourceFile  = "file"
	OffersSourceAdmin = "admin"
)

// Offer is one entry in host-config.yaml offers[].
type Offer struct {
	OfferingID string `yaml:"offering_id" json:"offering_id"`
	Capability string `yaml:"capability" json:"capability"`
	Protocol   string `yaml:"protocol" json:"protocol"`
	// Match selects attached runners by declared identity. Keys are
	// "identity.<dotted key>" (identity.openai.model); values are exact
	// strings. Empty matches every runner declaring the capability +
	// protocol.
	Match    map[string]string `yaml:"match,omitempty" json:"match,omitempty"`
	Price    Price             `yaml:"price" json:"price"`
	Capacity OfferCapacity     `yaml:"capacity,omitempty" json:"capacity,omitempty"`
	// Extra is operator-declared metadata (region, gpu_class). The
	// runner's frozen identity and any promoted x-* keys are merged into
	// the advertised extra at freeze time; a collision is a load error.
	Extra       map[string]any `yaml:"extra,omitempty" json:"extra,omitempty"`
	Constraints map[string]any `yaml:"constraints,omitempty" json:"constraints,omitempty"`
	// ExtraFromRunner lists x-* keys from the runner's attach document
	// that are promoted into the advertised extra (runner-attach §3.3).
	// Anything not listed is relayed to operator surfaces only.
	ExtraFromRunner []string `yaml:"extra_from_runner,omitempty" json:"extra_from_runner,omitempty"`
	// SessionPolicy carries the operator-owned commercial axes of a
	// paid-session offer. Absent means every default.
	SessionPolicy *SessionPolicy `yaml:"session_policy,omitempty" json:"session_policy,omitempty"`
	// Certification is the step list every matched runner must pass
	// (protocols/certification-steps.md). Empty certifies on match —
	// and therefore freezes on the first match.
	Certification []CertificationStep `yaml:"certification,omitempty" json:"certification,omitempty"`
	// RecertifyEverySeconds re-runs certification on eligible runners
	// periodically. 0 disables.
	RecertifyEverySeconds int `yaml:"recertify_every_seconds,omitempty" json:"recertify_every_seconds,omitempty"`
	// Disabled keeps the offer configured but neither advertised nor
	// dispatched (broker-admin §4.4).
	Disabled bool `yaml:"disabled,omitempty" json:"disabled,omitempty"`
}

// OfferCapacity is operator-owned; a runner never declares capacity
// (plan 0043 §8).
type OfferCapacity struct {
	// MaxInFlight bounds concurrent dispatches per eligible runner. 0
	// means the broker default.
	MaxInFlight int `yaml:"max_in_flight,omitempty" json:"max_in_flight,omitempty"`
	QueueLimit  int `yaml:"queue_limit,omitempty" json:"queue_limit,omitempty"`
}

// SessionPolicy is the operator side of the paid-session axes
// (offering-axes.md §3). The runner side — descriptor_schema, metering,
// heartbeat cadence, session_params_schema — comes from attach.
type SessionPolicy struct {
	Attachment           string           `yaml:"attachment,omitempty" json:"attachment,omitempty"` // external (the only value; offering-axes §3)
	Refill               string           `yaml:"refill,omitempty" json:"refill,omitempty"`         // extensible | bounded
	LeasePolicy          string           `yaml:"lease_policy,omitempty" json:"lease_policy,omitempty"`
	LeaseMaxSeconds      int              `yaml:"lease_max_seconds,omitempty" json:"lease_max_seconds,omitempty"`
	BurnRatePerSec       float64          `yaml:"burn_rate_per_second,omitempty" json:"burn_rate_per_second,omitempty"`
	MinRunwayUnits       int64            `yaml:"min_runway_units,omitempty" json:"min_runway_units,omitempty"`
	MaxRotations         int              `yaml:"max_rotations,omitempty" json:"max_rotations,omitempty"`
	ToleranceBandPct     float64          `yaml:"tolerance_band_pct,omitempty" json:"tolerance_band_pct,omitempty"`
	RunwayIncrementUnits int64            `yaml:"runway_increment_units,omitempty" json:"runway_increment_units,omitempty"`
	Heartbeat            SessionHeartbeat `yaml:"heartbeat,omitempty" json:"heartbeat,omitempty"`
}

// CertificationStep is one entry of an offer's certification[]
// (certification-steps.md §2). Config is type-tagged and validated per
// type below; its contents are handed to the step engine as-is.
type CertificationStep struct {
	Name      string         `yaml:"name" json:"name"`
	Type      string         `yaml:"type" json:"type"`
	Required  *bool          `yaml:"required,omitempty" json:"required,omitempty"` // nil means true
	TimeoutMS int            `yaml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
	Config    map[string]any `yaml:"config,omitempty" json:"config,omitempty"`
}

// IsRequired applies the default.
func (s CertificationStep) IsRequired() bool { return s.Required == nil || *s.Required }

var (
	offeringIDRE   = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,128}$`)
	matchKeyRE     = regexp.MustCompile(`^identity\.[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)*$`)
	stepNameRE     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	promotedKeyRE  = regexp.MustCompile(`^x-[A-Za-z0-9._-]+$`)
	fixtureRefRE   = regexp.MustCompile(`^[a-z0-9-]+/[A-Za-z0-9._-]+$`)
	relativePathRE = regexp.MustCompile(`^/`)
)

var validStepTypes = map[string]bool{"readiness": true, "request": true, "usage": true, "latency": true}

// Allowed config keys per step type — mirrors
// livepeer-network-protocol/protocols/certification-steps/schema.json.
// An unknown key is a load error, never a run-time surprise.
var stepConfigKeys = map[string]map[string]bool{
	"readiness": keySet("attempts", "interval_ms", "consecutive"),
	"request": keySet("transport", "path", "method", "headers", "body", "parts", "expect_status",
		"expect_content_type", "assert", "max_response_bytes",
		"session_params", "expect_descriptor_schema", "hold_ms", "expect_status_after_terminate"),
	"usage":   keySet("source", "min_units", "window_ms"),
	"latency": keySet("request", "samples", "warmup", "concurrency", "p50_max_ms", "p95_max_ms", "measure"),
}

func keySet(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// validateOffers checks offers[] and offers_source.
func (c *Config) validateOffers() error {
	switch c.OffersSource {
	case "":
		c.OffersSource = OffersSourceFile
	case OffersSourceFile:
	case OffersSourceAdmin:
		if c.AdminAuth.Method != "bearer" {
			return fmt.Errorf("offers_source: admin requires admin_auth.method=bearer (the controller pushes over /admin/v1/offers)")
		}
		if len(c.Offers) > 0 {
			return fmt.Errorf("offers_source: admin — offers[] must be empty in the file; the controller owns them")
		}
	default:
		return fmt.Errorf("offers_source %q is not supported (file|admin)", c.OffersSource)
	}

	seen := make(map[string]int, len(c.Offers))
	for i := range c.Offers {
		o := &c.Offers[i]
		ctx := fmt.Sprintf("offers[%d]", i)
		if o.OfferingID == "" {
			return fmt.Errorf("%s: offering_id is required", ctx)
		}
		ctx = fmt.Sprintf("offers[%d] (%s)", i, o.OfferingID)
		if !offeringIDRE.MatchString(o.OfferingID) {
			return fmt.Errorf("%s: offering_id must match %s", ctx, offeringIDRE)
		}
		if prev, dup := seen[o.OfferingID]; dup {
			return fmt.Errorf("%s: offering_id repeats offers[%d] — one offer per id; runners multiply an offer, entries do not", ctx, prev)
		}
		seen[o.OfferingID] = i
		if strings.TrimSpace(o.Capability) == "" {
			return fmt.Errorf("%s: capability is required", ctx)
		}
		isJob, isSession := o.Protocol == "paid-job/v1", o.Protocol == "paid-session/v1"
		if !isJob && !isSession {
			return fmt.Errorf("%s: protocol must be paid-job/v1 or paid-session/v1 (got %q)", ctx, o.Protocol)
		}
		if isSession {
			if c.SessionStore.Path == "" {
				return fmt.Errorf("%s: paid-session offers require session_store", ctx)
			}
			if c.ExternalBaseURL == "" {
				return fmt.Errorf("%s: paid-session offers require external_base_url", ctx)
			}
		}

		for k := range o.Match {
			if !matchKeyRE.MatchString(k) {
				return fmt.Errorf("%s: match key %q must be identity.<dotted key> (runner-attach §3.2)", ctx, k)
			}
			if o.Match[k] == "" {
				return fmt.Errorf("%s: match.%s must not be empty", ctx, k)
			}
		}

		if !priceWeiRE.MatchString(o.Price.AmountWei) {
			return fmt.Errorf("%s: price.amount_wei must be a non-negative decimal string (got %q)", ctx, o.Price.AmountWei)
		}
		if o.Price.PerUnits == 0 {
			return fmt.Errorf("%s: price.per_units must be > 0", ctx)
		}
		if amount, ok := new(big.Int).SetString(o.Price.AmountWei, 10); ok && !amount.IsInt64() {
			return fmt.Errorf("%s: price.amount_wei %s exceeds the payment wire's int64 range", ctx, o.Price.AmountWei)
		}
		if o.Capacity.MaxInFlight < 0 || o.Capacity.QueueLimit < 0 {
			return fmt.Errorf("%s: capacity values must be >= 0", ctx)
		}

		for _, reserved := range []string{"protocol", "job", "session"} {
			if _, clash := o.Extra[reserved]; clash {
				return fmt.Errorf("%s: extra.%s is reserved — the declaration owns that key", ctx, reserved)
			}
		}
		for k := range o.Extra {
			if strings.HasPrefix(k, "x-") {
				return fmt.Errorf("%s: extra.%s — x-* keys are runner extensions; promote them with extra_from_runner instead", ctx, k)
			}
		}
		if err := validateCapabilityID(ctx, o.Capability); err != nil {
			return err
		}
		if err := validateExtraGrammar(ctx, o.Capability, o.Extra); err != nil {
			return err
		}
		promoted := map[string]bool{}
		for _, k := range o.ExtraFromRunner {
			if !promotedKeyRE.MatchString(k) {
				return fmt.Errorf("%s: extra_from_runner %q must be an x-* key", ctx, k)
			}
			if k == "x-certification-suggested" {
				return fmt.Errorf("%s: extra_from_runner: x-certification-suggested is never promoted (runner-attach §3.3)", ctx)
			}
			if promoted[k] {
				return fmt.Errorf("%s: extra_from_runner repeats %q", ctx, k)
			}
			promoted[k] = true
		}

		if o.SessionPolicy != nil {
			if !isSession {
				return fmt.Errorf("%s: session_policy is only valid for paid-session/v1", ctx)
			}
			if err := validateSessionPolicy(o.SessionPolicy); err != nil {
				return fmt.Errorf("%s: session_policy: %w", ctx, err)
			}
		}
		if o.RecertifyEverySeconds < 0 {
			return fmt.Errorf("%s: recertify_every_seconds must be >= 0", ctx)
		}
		if err := validateCertification(o.Certification, isSession); err != nil {
			return fmt.Errorf("%s: certification: %w", ctx, err)
		}
	}
	return nil
}

func validateSessionPolicy(p *SessionPolicy) error {
	switch p.Attachment {
	case "", "external":
	default:
		return fmt.Errorf("attachment must be external (got %q)", p.Attachment)
	}
	switch p.Refill {
	case "", "extensible", "bounded":
	default:
		return fmt.Errorf("refill must be extensible or bounded (got %q)", p.Refill)
	}
	switch p.LeasePolicy {
	case "", "funding-tracking":
	case "fixed":
		if p.LeaseMaxSeconds <= 0 {
			return fmt.Errorf("lease_policy fixed requires lease_max_seconds > 0")
		}
	default:
		return fmt.Errorf("lease_policy must be funding-tracking or fixed (got %q)", p.LeasePolicy)
	}
	if p.LeaseMaxSeconds < 0 || p.MinRunwayUnits < 0 || p.MaxRotations < 0 ||
		p.ToleranceBandPct < 0 || p.RunwayIncrementUnits < 0 || p.BurnRatePerSec < 0 {
		return fmt.Errorf("numeric fields must be >= 0")
	}
	if p.Heartbeat.IntervalSeconds < 0 || p.Heartbeat.MissedThreshold < 0 {
		return fmt.Errorf("heartbeat values must be >= 0")
	}
	return nil
}

// validateCertification mirrors certification-steps/schema.json plus the
// cross-step rules (§2, §3.3, §3.4).
func validateCertification(steps []CertificationStep, isSession bool) error {
	if len(steps) > 32 {
		return fmt.Errorf("at most 32 steps")
	}
	names := map[string]bool{}
	sawRequest := false
	for i, s := range steps {
		ctx := fmt.Sprintf("[%d]", i)
		if !stepNameRE.MatchString(s.Name) {
			return fmt.Errorf("%s: name must match %s", ctx, stepNameRE)
		}
		ctx = fmt.Sprintf("[%d] %s", i, s.Name)
		if names[s.Name] {
			return fmt.Errorf("%s: name repeats", ctx)
		}
		names[s.Name] = true
		if !validStepTypes[s.Type] {
			return fmt.Errorf("%s: type must be readiness|request|usage|latency (got %q)", ctx, s.Type)
		}
		if s.TimeoutMS < 0 || s.TimeoutMS > 600000 {
			return fmt.Errorf("%s: timeout_ms must be 0..600000", ctx)
		}
		allowed := stepConfigKeys[s.Type]
		for _, k := range sortedKeys(s.Config) {
			if !allowed[k] {
				return fmt.Errorf("%s: config.%s is not a %s key (certification-steps §3)", ctx, k, s.Type)
			}
		}
		switch s.Type {
		case "request":
			if err := validateRequestConfig(s.Config, isSession); err != nil {
				return fmt.Errorf("%s: %w", ctx, err)
			}
			sawRequest = true
		case "usage":
			src, has := s.Config["source"]
			if inline, ok := src.(map[string]any); ok {
				if err := validateRequestConfig(inline, isSession); err != nil {
					return fmt.Errorf("%s: source: %w", ctx, err)
				}
			} else if has && src != "previous_request" {
				return fmt.Errorf("%s: source must be previous_request or a request config", ctx)
			} else if !sawRequest {
				return fmt.Errorf("%s: no preceding request step and no inline source (certification-steps §3.3)", ctx)
			}
			if n, ok := s.Config["min_units"]; ok && asInt(n) < 1 {
				return fmt.Errorf("%s: min_units must be >= 1", ctx)
			}
		case "latency":
			_, p50 := s.Config["p50_max_ms"]
			_, p95 := s.Config["p95_max_ms"]
			if !p50 && !p95 {
				return fmt.Errorf("%s: one of p50_max_ms / p95_max_ms is required", ctx)
			}
			req, has := s.Config["request"]
			if inline, ok := req.(map[string]any); ok {
				if err := validateRequestConfig(inline, isSession); err != nil {
					return fmt.Errorf("%s: request: %w", ctx, err)
				}
			} else if has && req != "previous_request" {
				return fmt.Errorf("%s: request must be previous_request or a request config", ctx)
			} else if !sawRequest {
				return fmt.Errorf("%s: no preceding request step and no inline request (certification-steps §3.4)", ctx)
			}
			if n, ok := s.Config["samples"]; ok && (asInt(n) < 1 || asInt(n) > 20) {
				return fmt.Errorf("%s: samples must be 1..20", ctx)
			}
			if m, ok := s.Config["measure"]; ok && m != "total" && m != "first_byte" {
				return fmt.Errorf("%s: measure must be total or first_byte", ctx)
			}
		}
	}
	return nil
}

func validateRequestConfig(cfg map[string]any, isSession bool) error {
	for _, k := range sortedKeys(cfg) {
		if !stepConfigKeys["request"][k] {
			return fmt.Errorf("config.%s is not a request key", k)
		}
	}
	_, hasParts := cfg["parts"]
	_, hasBody := cfg["body"]
	_, hasSessionParams := cfg["session_params"]
	transport, _ := cfg["transport"].(string)
	if isSession {
		if hasParts || hasBody || transport != "" {
			return fmt.Errorf("paid-session request steps take session_params, not transport/body/parts")
		}
	} else if hasSessionParams {
		return fmt.Errorf("session_params is only valid on paid-session offers")
	}
	switch transport {
	case "", "unary", "stream", "multipart":
	default:
		return fmt.Errorf("transport must be unary|stream|multipart (got %q)", transport)
	}
	if hasParts && transport != "multipart" {
		return fmt.Errorf("parts requires transport: multipart")
	}
	if transport == "multipart" && hasBody {
		return fmt.Errorf("multipart takes parts, not body")
	}
	if p, ok := cfg["path"].(string); ok && (!relativePathRE.MatchString(p) || strings.Contains(p, "..") || strings.ContainsAny(p, "?#") || strings.Contains(p, "://")) {
		return fmt.Errorf("path must be relative (/...) with no .., query, fragment, or scheme")
	}
	if h, ok := cfg["headers"].(map[string]any); ok {
		for k := range h {
			if strings.HasPrefix(strings.ToLower(k), "livepeer-") {
				return fmt.Errorf("headers.%s: Livepeer-* headers are forbidden in certification requests", k)
			}
		}
	}
	if parts, ok := cfg["parts"].([]any); ok {
		for i, raw := range parts {
			part, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("parts[%d] must be a mapping", i)
			}
			if _, ok := part["name"].(string); !ok {
				return fmt.Errorf("parts[%d].name is required", i)
			}
			_, hasValue := part["value"]
			fx, hasFixture := part["fixture"]
			if hasValue == hasFixture {
				return fmt.Errorf("parts[%d]: exactly one of value / fixture", i)
			}
			if hasFixture {
				f, ok := fx.(map[string]any)
				if !ok {
					return fmt.Errorf("parts[%d].fixture must be a mapping", i)
				}
				ref, hasRef := f["ref"].(string)
				_, hasInline := f["inline_base64"]
				if hasRef == hasInline {
					return fmt.Errorf("parts[%d].fixture: exactly one of ref / inline_base64", i)
				}
				if hasRef && !fixtureRefRE.MatchString(ref) {
					return fmt.Errorf("parts[%d].fixture.ref must be <dir>/<name>", i)
				}
				if hasInline {
					if _, ok := f["content_type"].(string); !ok {
						return fmt.Errorf("parts[%d].fixture.inline_base64 requires content_type", i)
					}
				}
			}
		}
	}
	return nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case uint64:
		return int(n)
	}
	return -1
}
