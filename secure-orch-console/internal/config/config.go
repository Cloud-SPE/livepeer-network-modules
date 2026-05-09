// Package config holds operator-supplied console configuration.
// Keystore selector, listen address, last-signed + audit log paths.
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
	Keystore        Keystore
	LastSignedPath  string
	AuditLogPath    string
	AuditRotateSize int64
	Listen          string
	ProtocolSocket  string
	AdminTokens     []string
}

// Keystore selects the V3 JSON keystore backing the signer.
type Keystore struct {
	Path         string
	PasswordFile string
}

// ParseKeystore parses a --keystore flag value of the form `v3:<path>`.
// The selector prefix is retained so a later release can introduce a
// hardware backend without breaking the flag surface.
func ParseKeystore(s, passwordFile string) (Keystore, error) {
	if s == "" {
		return Keystore{}, errors.New("config: --keystore is required")
	}
	idx := strings.Index(s, ":")
	if idx < 1 {
		return Keystore{}, fmt.Errorf("config: --keystore must be v3:<path>, got %q", s)
	}
	kind := s[:idx]
	value := s[idx+1:]
	if kind != "v3" {
		return Keystore{}, fmt.Errorf("config: unsupported keystore kind %q (only v3 is supported)", kind)
	}
	if value == "" {
		return Keystore{}, errors.New("config: v3 keystore requires a path")
	}
	return Keystore{Path: value, PasswordFile: passwordFile}, nil
}

// Validate confirms required fields are populated and the listen
// address is syntactically valid.
func (c Config) Validate() error {
	if c.LastSignedPath == "" {
		return errors.New("config: --last-signed is required")
	}
	if c.AuditLogPath == "" {
		return errors.New("config: --audit-log is required")
	}
	if c.AuditRotateSize < 0 {
		return errors.New("config: --audit-rotate-size must not be negative")
	}
	if err := ValidateListenAddr(c.Listen); err != nil {
		return err
	}
	return nil
}

// ValidateListenAddr confirms that --listen is an explicit host:port
// pair. The operator chooses whether to bind loopback-only or expose
// the console on a wider interface; the binary only rejects ambiguous
// all-interface shorthand such as :8080.
func ValidateListenAddr(addr string) error {
	if addr == "" {
		return errors.New("config: --listen is required and must be host:port")
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
	return nil
}
