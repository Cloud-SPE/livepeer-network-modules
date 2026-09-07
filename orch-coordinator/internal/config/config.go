// Package config defines the orch-coordinator's coordinator-config.yaml
// grammar plus the boot-time validation pass. The grammar is small by
// design: orch identity, broker list, publish tunables.
//
// The runtime flag set lives in cmd/livepeer-orch-coordinator and is
// orthogonal to the YAML — flags pin per-process behavior (listen
// addresses, log level, dev mode) while the YAML pins per-deployment
// data (which brokers, what eth_address to expect, manifest TTL).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level coordinator-config.yaml schema.
type Config struct {
	Identity Identity `yaml:"identity"`
	Brokers  []Broker `yaml:"brokers"`
	Publish  Publish  `yaml:"publish,omitempty"`
	// SettlementKeys are the hot keys the cold key delegates settlement
	// signing to (manifest spec 2.3.0, `settlement_keys[]`). Each broker
	// signs its settlement records with one of these; a clearinghouse
	// trusts the record only if the published manifest lists the
	// recovered key with a window containing the record's issued_at.
	// Absent or empty means no delegation is published, which is what
	// every manifest before this field carried.
	SettlementKeys []SettlementKey `yaml:"settlement_keys,omitempty"`
}

// Identity carries the orch's chain identity. Must be present.
type Identity struct {
	OrchEthAddress string `yaml:"orch_eth_address"`
}

// Broker names a single capability-broker on the LAN.
type Broker struct {
	Name    string `yaml:"name"`
	BaseURL string `yaml:"base_url"`
	// AdminTokenRef points at the broker's admin bearer, which the
	// hot-zone console uses to read runners and offers and to make the
	// operator's write gestures (plan 0043 §3.6). Reference form only —
	// `env://VAR` or `file:///path` — never the secret inline: this
	// token can change what a runner may serve, so it gets the same
	// handling as the agent bearer. A broker without one is read-only
	// from the console's point of view, and says so.
	AdminTokenRef string `yaml:"admin_token_ref,omitempty"`
}

// ResolveAdminToken reads the referenced secret. An empty ref yields an
// empty token and no error: not configuring the console for a broker is
// a choice, not a failure.
func (b Broker) ResolveAdminToken() (string, error) {
	ref := strings.TrimSpace(b.AdminTokenRef)
	switch {
	case ref == "":
		return "", nil
	case strings.HasPrefix(ref, "env://"):
		return strings.TrimSpace(os.Getenv(strings.TrimPrefix(ref, "env://"))), nil
	case strings.HasPrefix(ref, "file://"):
		raw, err := os.ReadFile(strings.TrimPrefix(ref, "file://"))
		if err != nil {
			return "", fmt.Errorf("read admin token: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	default:
		return "", fmt.Errorf("admin_token_ref %q: want env:// or file://", ref)
	}
}

// SettlementKey is one delegated settlement-signing key and its
// validity window. Mirrors livepeer-network-protocol/manifest/schema.json
// #/$defs/settlement_key; Label is operator-only and never published.
type SettlementKey struct {
	// PublicKey is the uncompressed secp256k1 public key: 0x04 followed
	// by 128 hex characters (X || Y). Normalized to lower case on
	// validation, because the schema pattern is lower-case only.
	PublicKey string    `yaml:"public_key"`
	NotBefore time.Time `yaml:"not_before"`
	ExpiresAt time.Time `yaml:"expires_at"`
	Label     string    `yaml:"label,omitempty"`
}

// Publish holds tunables that affect manifest output. Optional; the
// flag set carries deployment-wide defaults when this block is absent.
type Publish struct {
	ManifestTTL time.Duration `yaml:"manifest_ttl,omitempty"`
	// SettlementKeyValidity is the window the coordinator assigns to a
	// settlement key a broker announces without one (see
	// settlement_keys for the pinned form). Zero means one year. The
	// window re-anchors at two thirds elapsed, which is a change the
	// cold key reviews — renewal is a sign cycle, never a lapse.
	SettlementKeyValidity time.Duration `yaml:"settlement_key_validity,omitempty"`
}

// Load reads a YAML file from disk and validates it.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: validate %s: %w", path, err)
	}
	return &cfg, nil
}

// LoadBytes parses + validates from an in-memory buffer. Used by tests
// and the dev-mode synthetic config path.
func LoadBytes(raw []byte) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: validate: %w", err)
	}
	return &cfg, nil
}

// Validate checks structural and semantic invariants. Returns the first
// failure as an error.
func (c *Config) Validate() error {
	if err := validateEthAddress(c.Identity.OrchEthAddress); err != nil {
		return fmt.Errorf("identity.orch_eth_address: %w", err)
	}
	if len(c.Brokers) == 0 {
		return errors.New("brokers: at least one broker is required")
	}
	seen := make(map[string]struct{}, len(c.Brokers))
	for i, b := range c.Brokers {
		if b.Name == "" {
			return fmt.Errorf("brokers[%d].name: required", i)
		}
		if _, dup := seen[b.Name]; dup {
			return fmt.Errorf("brokers[%d].name %q: duplicate", i, b.Name)
		}
		seen[b.Name] = struct{}{}
		if err := validateBaseURL(b.BaseURL); err != nil {
			return fmt.Errorf("brokers[%d].base_url: %w", i, err)
		}
		ref := strings.TrimSpace(b.AdminTokenRef)
		if ref != "" && !strings.HasPrefix(ref, "env://") && !strings.HasPrefix(ref, "file://") {
			return fmt.Errorf("brokers[%d].admin_token_ref %q: want env:// or file://", i, ref)
		}
	}
	if c.Publish.ManifestTTL < 0 {
		return errors.New("publish.manifest_ttl: must be non-negative")
	}
	if c.Publish.SettlementKeyValidity < 0 {
		return errors.New("publish.settlement_key_validity: must be non-negative")
	}
	if v := c.Publish.SettlementKeyValidity; v > 0 && v < 24*time.Hour {
		return errors.New("publish.settlement_key_validity: must be at least 24h; a shorter window churns the manifest")
	}
	seenKeys := make(map[string]int, len(c.SettlementKeys))
	for i := range c.SettlementKeys {
		k := &c.SettlementKeys[i]
		normalized, err := normalizeSettlementPublicKey(k.PublicKey)
		if err != nil {
			return fmt.Errorf("settlement_keys[%d].public_key: %w", i, err)
		}
		k.PublicKey = normalized
		if j, dup := seenKeys[normalized]; dup {
			return fmt.Errorf("settlement_keys[%d].public_key: duplicate of settlement_keys[%d]", i, j)
		}
		seenKeys[normalized] = i
		if k.NotBefore.IsZero() {
			return fmt.Errorf("settlement_keys[%d].not_before: required (RFC 3339)", i)
		}
		if k.ExpiresAt.IsZero() {
			return fmt.Errorf("settlement_keys[%d].expires_at: required (RFC 3339)", i)
		}
		if !k.ExpiresAt.After(k.NotBefore) {
			return fmt.Errorf("settlement_keys[%d]: expires_at %s must be after not_before %s",
				i, k.ExpiresAt.UTC().Format(time.RFC3339), k.NotBefore.UTC().Format(time.RFC3339))
		}
	}
	return nil
}

// settlementPublicKeyHexLen is 0x04 || X || Y: one prefix byte and two
// 32-byte coordinates, as hex.
const settlementPublicKeyHexLen = 130

// normalizeSettlementPublicKey checks the uncompressed-secp256k1 shape
// the manifest schema requires and returns the lower-case form the
// schema pattern matches.
func normalizeSettlementPublicKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return "", errors.New("must be 0x-prefixed")
	}
	body := s[2:]
	if len(body) != settlementPublicKeyHexLen {
		return "", fmt.Errorf("must be 0x + %d hex chars (uncompressed secp256k1), got %d", settlementPublicKeyHexLen, len(body))
	}
	for _, c := range body {
		if !isHexDigit(c) {
			return "", errors.New("must be valid hex")
		}
	}
	body = strings.ToLower(body)
	if !strings.HasPrefix(body, "04") {
		return "", errors.New("must be an uncompressed key (0x04 prefix byte)")
	}
	return "0x" + body, nil
}

// EthAddress returns the canonicalized lower-case orch eth address.
func (c *Config) EthAddress() string {
	return strings.ToLower(strings.TrimSpace(c.Identity.OrchEthAddress))
}

func validateEthAddress(s string) error {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return errors.New("must be 0x-prefixed")
	}
	body := s[2:]
	if len(body) != 40 {
		return fmt.Errorf("must be 0x + 40 hex chars, got %d", len(s))
	}
	for _, c := range body {
		if !isHexDigit(c) {
			return errors.New("must be valid hex")
		}
	}
	return nil
}

func validateBaseURL(s string) error {
	if s == "" {
		return errors.New("required")
	}
	_, err := NormalizeOptionalBaseURL(s)
	return err
}

func NormalizeOptionalBaseURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("parse %q: %w", s, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("host is required")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("query and fragment are not allowed")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

func isHexDigit(c rune) bool {
	switch {
	case c >= '0' && c <= '9':
		return true
	case c >= 'a' && c <= 'f':
		return true
	case c >= 'A' && c <= 'F':
		return true
	}
	return false
}
