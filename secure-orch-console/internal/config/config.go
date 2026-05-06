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
	Keystore       Keystore
	LastSignedPath string
	AuditLogPath   string
	Listen         string
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
// address is loopback-only.
func (c Config) Validate() error {
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
// rule. Accepted: 127.0.0.1:<port>, [::1]:<port>, localhost:<port>.
// Rejected: empty host, 0.0.0.0, any non-loopback IP, any non-localhost
// hostname.
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
	if strings.EqualFold(host, "localhost") {
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
