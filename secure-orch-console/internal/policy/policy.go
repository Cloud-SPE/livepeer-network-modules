// Package policy is the plan 0042 sign-policy engine: the policy
// file the operator authors, the change classifier that grades a
// candidate-vs-last-signed diff, the decision function that maps a
// class through the policy to an action, and the auto-sign rate
// limiter.
//
// Everything here is pure (or, for the rate limiter, clock-injected)
// so the agent loop can be tested around it. The package never
// touches the signer, the socket, or the audit log — the agent loop
// owns side effects.
package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Version is the policy_version this engine understands. A policy
// file declaring anything else fails validation — fail closed, never
// guess at a future schema.
const Version = 1

// OnBreachPause is the only rate-limit breach behavior in v1:
// breaching the hourly cap stops all auto-signing (including
// renewals) until the operator clears it.
const OnBreachPause = "pause"

// Policy is the operator-authored sign policy
// (/etc/secure-orch/sign-policy.json, plan 0042 §7). The JSON is
// strict: unknown fields are a validation error, so a typo'd knob
// can never silently fall back to a default.
type Policy struct {
	PolicyVersion int          `json:"policy_version"`
	AutoSign      AutoSign     `json:"auto_sign"`
	BenignBounds  BenignBounds `json:"benign_bounds"`
	RateLimit     RateLimit    `json:"rate_limit"`
	// StabilityWindowSeconds is how long a pulled candidate's ETag
	// must stay unchanged before the agent acts on it (§6 step 3).
	StabilityWindowSeconds int `json:"stability_window_seconds"`
	// RenewalThresholdFraction × manifest TTL is the remaining
	// validity below which an unchanged candidate classifies as
	// renewal instead of no_op (§6 step 5). Must match the
	// coordinator's --renewal-threshold or renewals arrive before
	// the agent considers them due; keeping both at the 1/3 default
	// keeps them aligned.
	RenewalThresholdFraction float64 `json:"renewal_threshold_fraction"`
}

// AutoSign holds the two dials. Phase 1 ships renewal=true,
// benign=false; phase 2 is the one-line benign=true flip.
type AutoSign struct {
	Renewal bool `json:"renewal"`
	Benign  bool `json:"benign"`
}

// BenignBounds bounds what counts as a benign change (§7). Outside
// any bound the change grades critical and holds for the operator.
type BenignBounds struct {
	// PriceDeltaMaxPct bounds price changes in either direction.
	// Decreases are bounded too (plan 0042 Q1 proposal): a
	// fat-fingered 99% price cut holds for review instead of
	// auto-signing.
	PriceDeltaMaxPct float64 `json:"price_delta_max_pct"`
	// AllowTupleRemoval treats a disappearing (capability, offering)
	// tuple as benign — removal reduces exposure.
	AllowTupleRemoval bool `json:"allow_tuple_removal"`
	// WorkerURLDomainAllowlist is an explicit suffix list (no
	// wildcards, plan 0042 Q2 proposal): a changed worker_url is
	// benign only when its host equals an entry or ends with
	// "."+entry.
	WorkerURLDomainAllowlist []string `json:"worker_url_domain_allowlist"`
}

// RateLimit caps auto-sign frequency (§7). A burst of auto-signs is
// the loudest available signal that the coordinator side is
// misbehaving.
type RateLimit struct {
	MaxAutoSignsPerHour int    `json:"max_auto_signs_per_hour"`
	OnBreach            string `json:"on_breach"`
}

// Loaded couples a validated policy with the SHA-256 of the exact
// file bytes it came from; the hash is recorded in audit on every
// load (§7).
type Loaded struct {
	Policy Policy
	// SHA256 is the lower-case hex digest of the policy file bytes.
	SHA256 string
}

// Load reads, strictly parses, and validates the policy file. Any
// failure means the caller must pause auto-signing (fail closed) —
// there is no fallback to a previous or default policy.
func Load(path string) (Loaded, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Loaded{}, fmt.Errorf("policy: read %s: %w", path, err)
	}
	p, err := Parse(body)
	if err != nil {
		return Loaded{}, fmt.Errorf("policy: %s: %w", path, err)
	}
	sum := sha256.Sum256(body)
	return Loaded{Policy: p, SHA256: hex.EncodeToString(sum[:])}, nil
}

// Parse strictly decodes and validates policy bytes.
func Parse(body []byte) (Policy, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var p Policy
	if err := dec.Decode(&p); err != nil {
		return Policy{}, fmt.Errorf("parse: %w", err)
	}
	// A second document in the file is as suspect as an unknown key.
	if dec.More() {
		return Policy{}, errors.New("parse: trailing content after policy object")
	}
	if err := p.Validate(); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// Validate enforces the schema constraints. Errors name the exact
// field so the operator can fix the file from the alert alone.
func (p Policy) Validate() error {
	if p.PolicyVersion != Version {
		return fmt.Errorf("policy_version must be %d, got %d", Version, p.PolicyVersion)
	}
	if p.BenignBounds.PriceDeltaMaxPct < 0 || p.BenignBounds.PriceDeltaMaxPct > 100 {
		return fmt.Errorf("benign_bounds.price_delta_max_pct must be in [0,100], got %v", p.BenignBounds.PriceDeltaMaxPct)
	}
	for _, entry := range p.BenignBounds.WorkerURLDomainAllowlist {
		if err := validateAllowlistEntry(entry); err != nil {
			return fmt.Errorf("benign_bounds.worker_url_domain_allowlist: %w", err)
		}
	}
	if p.RateLimit.MaxAutoSignsPerHour < 1 {
		return fmt.Errorf("rate_limit.max_auto_signs_per_hour must be >= 1, got %d (an unbounded auto-sign rate is not expressible)", p.RateLimit.MaxAutoSignsPerHour)
	}
	if p.RateLimit.OnBreach != OnBreachPause {
		return fmt.Errorf("rate_limit.on_breach must be %q, got %q", OnBreachPause, p.RateLimit.OnBreach)
	}
	if p.StabilityWindowSeconds < 0 {
		return fmt.Errorf("stability_window_seconds must be >= 0, got %d", p.StabilityWindowSeconds)
	}
	if p.RenewalThresholdFraction <= 0 || p.RenewalThresholdFraction >= 1 {
		return fmt.Errorf("renewal_threshold_fraction must be in (0,1), got %v", p.RenewalThresholdFraction)
	}
	return nil
}

func validateAllowlistEntry(entry string) error {
	if entry == "" {
		return errors.New("empty entry")
	}
	if entry != strings.ToLower(entry) {
		return fmt.Errorf("entry %q must be lower-case", entry)
	}
	if strings.ContainsAny(entry, "*/ ") || strings.Contains(entry, "://") {
		return fmt.Errorf("entry %q must be a bare domain suffix (no wildcards, schemes, paths, or spaces)", entry)
	}
	if strings.HasPrefix(entry, ".") || strings.HasSuffix(entry, ".") {
		return fmt.Errorf("entry %q must not have leading/trailing dots", entry)
	}
	return nil
}
