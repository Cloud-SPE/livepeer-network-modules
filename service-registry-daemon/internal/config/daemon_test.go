package config

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultDaemon_DefaultsAreSane(t *testing.T) {
	d := DefaultDaemon()
	if d.RoundPollInterval != 1*time.Minute {
		t.Fatalf("default round-poll-interval: %s", d.RoundPollInterval)
	}
	if d.Discovery != DiscoveryChain {
		t.Fatalf("default discovery: %s", d.Discovery)
	}
	if d.ManifestMaxBytes != 4*1024*1024 {
		t.Fatalf("default manifest-max-bytes: %d", d.ManifestMaxBytes)
	}
	if !d.RejectUnsigned {
		t.Fatal("default reject-unsigned should be true")
	}
}

func TestDaemonValidate_RejectsCases(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Daemon)
		wantSub string
	}{
		{"no-mode", func(d *Daemon) {}, "--mode must be"},
		{"bad-mode", func(d *Daemon) { d.Mode = "bogus" }, "--mode must be"},
		{"resolver-no-socket", func(d *Daemon) { d.Mode = ModeResolver; d.SocketPath = "" }, "--socket"},
		{"resolver-zero-poll", func(d *Daemon) { d.Mode = ModeResolver; d.RoundPollInterval = 0 }, "round-poll-interval"},
		{"resolver-bad-discovery", func(d *Daemon) { d.Mode = ModeResolver; d.Discovery = "bogus" }, "--discovery"},
		{"resolver-tiny-bytes", func(d *Daemon) { d.Mode = ModeResolver; d.ManifestMaxBytes = 100 }, "manifest-max-bytes must be"},
		{"resolver-huge-bytes", func(d *Daemon) { d.Mode = ModeResolver; d.ManifestMaxBytes = 20 << 20 }, "capped at 16 MiB"},
		{"publisher-no-keystore", func(d *Daemon) { d.Mode = ModePublisher; d.Dev = false }, "keystore-path"},
		{"publisher-no-password", func(d *Daemon) { d.Mode = ModePublisher; d.Dev = false; d.KeystorePath = "/x" }, "keystore password"},
		{"dev-and-rpc", func(d *Daemon) { d.Mode = ModeResolver; d.Dev = true; d.ChainRPC = "https://x" }, "mutually exclusive"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := DefaultDaemon()
			c.mutate(d)
			err := d.Validate()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("err %q does not contain %q", err, c.wantSub)
			}
		})
	}
}

func TestDaemonValidate_ResolverHappy(t *testing.T) {
	d := DefaultDaemon()
	d.Mode = ModeResolver
	if err := d.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDaemonValidate_PublisherDevHappy(t *testing.T) {
	d := DefaultDaemon()
	d.Mode = ModePublisher
	d.Dev = true
	d.ChainRPC = ""
	if err := d.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
