package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

func writeSeed(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "seed.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The seed is what lets a hermetic run resolve through the SIGNED path:
// it points the resolver at a locally served manifest, which is then
// fetched and signature-verified exactly as a chain-discovered one is.
// Overlay-only, the other chain-free mode, is unsigned by construction.
func TestSeedChainPreloadsServiceURIs(t *testing.T) {
	path := writeSeed(t, `
seed:
  - eth_address: "0xabc0000000000000000000000000000000000000"
    service_uri: "http://127.0.0.1:9099/.well-known/livepeer-registry.json"
`)
	mem := chain.NewInMemory("")
	if err := seedChain(mem, path); err != nil {
		t.Fatalf("seedChain: %v", err)
	}
	addr, err := types.ParseEthAddress("0xabc0000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	uri, err := mem.GetServiceURI(t.Context(), addr)
	if err != nil {
		t.Fatalf("GetServiceURI: %v", err)
	}
	if !strings.HasPrefix(uri, "http://127.0.0.1:9099/") {
		t.Fatalf("seeded uri = %q", uri)
	}
}

// No seed is the previous behavior and stays valid.
func TestSeedChainEmptyPathIsNotAnError(t *testing.T) {
	if err := seedChain(chain.NewInMemory(""), ""); err != nil {
		t.Fatalf("empty seed path = %v; want nil", err)
	}
}

// A typo in a seed file must fail at boot. A hermetic CI run that starts
// with a misspelled key and resolves nothing looks like a broken fixture
// for as long as it takes someone to read the YAML.
func TestSeedChainRejectsUnknownField(t *testing.T) {
	path := writeSeed(t, `
seed:
  - eth_address: "0xabc0000000000000000000000000000000000000"
    serviceuri: "http://127.0.0.1:9099/m.json"
`)
	if err := seedChain(chain.NewInMemory(""), path); err == nil {
		t.Fatal("expected an error for an unknown field")
	}
}

func TestSeedChainRejectsIncompleteEntries(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"bad address", "seed:\n  - eth_address: \"nope\"\n    service_uri: \"http://x/m.json\"\n"},
		{"empty uri", "seed:\n  - eth_address: \"0xabc0000000000000000000000000000000000000\"\n    service_uri: \"\"\n"},
		{"no entries", "seed: []\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := seedChain(chain.NewInMemory(""), writeSeed(t, tc.body)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestSeedChainMissingFileIsAnError(t *testing.T) {
	if err := seedChain(chain.NewInMemory(""), "/nonexistent/seed.yaml"); err == nil {
		t.Fatal("expected an error for a missing seed file")
	}
}

// --dev used to force discovery=overlay-only unconditionally, and
// validation refuses --chain-seed alongside overlay-only — so the
// documented seed invocation could not start at all. The two rules were
// each correct and jointly fatal, which is the kind of thing only an
// end-to-end start catches.
func TestParseFlags_ChainSeedKeepsChainDiscovery(t *testing.T) {
	seed := writeSeed(t, `
seed:
  - eth_address: "0xabc0000000000000000000000000000000000000"
    service_uri: "http://127.0.0.1:9099/.well-known/livepeer-registry.json"
`)
	cfg, _, err := parseFlags([]string{"--mode=resolver", "--dev", "--chain-seed=" + seed})
	if err != nil {
		t.Fatalf("the documented seed invocation does not parse: %v", err)
	}
	if cfg.Discovery != config.DiscoveryChain {
		t.Fatalf("discovery = %q; want %q — a seed is read through the chain provider, and "+
			"overlay-only never reads it", cfg.Discovery, config.DiscoveryChain)
	}
	if cfg.ChainSeedPath != seed {
		t.Fatalf("chain seed path = %q; want %q", cfg.ChainSeedPath, seed)
	}
}

// Without a seed, --dev still means overlay-only.
func TestParseFlags_DevWithoutSeedStaysOverlayOnly(t *testing.T) {
	cfg, _, err := parseFlags([]string{"--mode=resolver", "--dev"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Discovery != config.DiscoveryOverlayOnly {
		t.Fatalf("discovery = %q; want %q for plain --dev", cfg.Discovery, config.DiscoveryOverlayOnly)
	}
}

// An explicit contradiction is still refused: overlay-only never reads
// the chain, so the seed would be silently ignored.
func TestParseFlags_ChainSeedWithExplicitOverlayOnlyRefused(t *testing.T) {
	seed := writeSeed(t, `
seed:
  - eth_address: "0xabc0000000000000000000000000000000000000"
    service_uri: "http://127.0.0.1:9099/m.json"
`)
	if _, _, err := parseFlags([]string{
		"--mode=resolver", "--dev", "--discovery=overlay-only", "--chain-seed=" + seed,
	}); err == nil {
		t.Fatal("expected --chain-seed with an explicit --discovery=overlay-only to be refused")
	}
}

// The daemon must actually START on the documented invocation. The unit
// tests above would all have passed while the binary refused to boot.
func TestRun_ChainSeedResolverStartsAndStops(t *testing.T) {
	seed := writeSeed(t, `
seed:
  - eth_address: "0xabc0000000000000000000000000000000000000"
    service_uri: "http://127.0.0.1:9099/.well-known/livepeer-registry.json"
`)
	sock := filepath.Join(t.TempDir(), "registry.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	err := run(ctx, []string{"--mode=resolver", "--dev", "--chain-seed=" + seed, "--socket=" + sock})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("the documented seed invocation does not start: %v", err)
	}
}

// A malformed seed must fail the daemon at boot rather than starting a
// resolver that silently resolves nothing.
func TestRun_ChainSeedMalformedFailsBoot(t *testing.T) {
	seed := writeSeed(t, "seed:\n  - eth_address: \"not-an-address\"\n    service_uri: \"http://x/m.json\"\n")
	sock := filepath.Join(t.TempDir(), "registry.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	err := run(ctx, []string{"--mode=resolver", "--dev", "--chain-seed=" + seed, "--socket=" + sock})
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a malformed seed started the daemon anyway (err=%v)", err)
	}
}
