// Package config holds operator-supplied console configuration.
// Keystore selector, spool dirs, listen address, audit log path.
// Today config is flag-driven; a config file is a candidate for a
// future commit if the surface grows.
package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// Config carries everything the console needs to boot. Fields are
// validated by Validate.
type Config struct {
	Keystore       Keystore
	InboxDir       string
	OutboxDir      string
	LastSignedPath string
	AuditLogPath   string
	Listen         string
}

// Keystore selects which signer implementation backs the console.
// The Path / ConnectorURL fields are union-discriminated by Kind.
type Keystore struct {
	Kind         KeystoreKind
	Path         string
	ConnectorURL string
	PasswordFile string
}

// KeystoreKind enumerates the signer backends. V3 is the baseline;
// YubiHSM 2 lights up in commit 6.
type KeystoreKind string

const (
	KeystoreV3      KeystoreKind = "v3"
	KeystoreYubiHSM KeystoreKind = "yubihsm"
)

// ParseKeystore parses a --keystore flag value of the form
// `<kind>:<value>`. Examples:
//
//	v3:/var/lib/secure-orch/keystore.json
//	yubihsm:tcp://127.0.0.1:12345
func ParseKeystore(s, passwordFile string) (Keystore, error) {
	if s == "" {
		return Keystore{}, errors.New("config: --keystore is required")
	}
	idx := strings.Index(s, ":")
	if idx < 1 {
		return Keystore{}, fmt.Errorf("config: --keystore must be <kind>:<value>, got %q", s)
	}
	kind := KeystoreKind(s[:idx])
	value := s[idx+1:]
	switch kind {
	case KeystoreV3:
		if value == "" {
			return Keystore{}, errors.New("config: v3 keystore requires a path")
		}
		return Keystore{Kind: KeystoreV3, Path: value, PasswordFile: passwordFile}, nil
	case KeystoreYubiHSM:
		if value == "" {
			return Keystore{}, errors.New("config: yubihsm keystore requires a connector URL")
		}
		return Keystore{Kind: KeystoreYubiHSM, ConnectorURL: value}, nil
	default:
		return Keystore{}, fmt.Errorf("config: unknown keystore kind %q (want v3 or yubihsm)", kind)
	}
}

// Validate confirms the listen address is loopback-only and required
// fields are populated.
func (c Config) Validate() error {
	if c.InboxDir == "" {
		return errors.New("config: --inbox is required")
	}
	if c.OutboxDir == "" {
		return errors.New("config: --outbox is required")
	}
	if c.LastSignedPath == "" {
		return errors.New("config: --last-signed is required")
	}
	if c.AuditLogPath == "" {
		return errors.New("config: --audit-log is required")
	}
	if err := ValidateLoopbackAddr(c.Listen); err != nil {
		return err
	}
	return nil
}

// ValidateLoopbackAddr is the bind-address gate enforcing the hard
// rule (plan 0019 §6.1.1). Accepted: 127.0.0.1:<port>,
// [::1]:<port>, localhost:<port>. Rejected: empty host, 0.0.0.0,
// any non-loopback IP, any non-localhost hostname.
func ValidateLoopbackAddr(addr string) error {
	if addr == "" {
		return errors.New("config: --listen is required and must be 127.0.0.1:<port>")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("config: --listen %q: %w", addr, err)
	}
	if host == "" {
		return fmt.Errorf("config: --listen %q has empty host (would bind all interfaces)", addr)
	}
	if port == "" {
		return fmt.Errorf("config: --listen %q has empty port", addr)
	}
	switch strings.ToLower(host) {
	case "localhost":
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("config: --listen host %q is not a literal IP or 'localhost'", host)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("config: --listen host %q must be a loopback address (hard rule: secure-orch never accepts inbound connections)", host)
	}
	return nil
}
