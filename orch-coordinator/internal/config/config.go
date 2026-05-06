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
}

// Identity carries the orch's chain identity. Must be present.
type Identity struct {
	OrchEthAddress string `yaml:"orch_eth_address"`
}

// Broker names a single capability-broker on the LAN.
type Broker struct {
	Name    string `yaml:"name"`
	BaseURL string `yaml:"base_url"`
}

// Publish holds tunables that affect manifest output. Optional; the
// flag set carries deployment-wide defaults when this block is absent.
type Publish struct {
	ManifestTTL time.Duration `yaml:"manifest_ttl,omitempty"`
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
	}
	if c.Publish.ManifestTTL < 0 {
		return errors.New("publish.manifest_ttl: must be non-negative")
	}
	return nil
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
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("parse %q: %w", s, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("host is required")
	}
	return nil
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
