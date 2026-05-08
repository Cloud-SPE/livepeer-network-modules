package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/manifestfetcher"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/verifier"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/repo/manifestcache"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/service/resolver"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

func TestParseFlags_HelpAsked(_ *testing.T) {
	// `-h` either sets helpAsked or returns flag.ErrHelp. Either path
	// is acceptable; the test only asserts that parseFlags doesn't
	// panic when -h is given.
	_, _, _ = parseFlags([]string{"-h"})
}

func TestParseFlags_RequiresMode(t *testing.T) {
	_, _, err := parseFlags([]string{})
	if err == nil {
		t.Fatal("expected validation error without --mode")
	}
	if !strings.Contains(err.Error(), "--mode") {
		t.Fatalf("err: %v", err)
	}
}

func TestParseFlags_ResolverDevHappy(t *testing.T) {
	cfg, _, err := parseFlags([]string{"--mode=resolver", "--dev"})
	if err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
	if !cfg.Dev {
		t.Fatal("expected Dev=true")
	}
}

func TestParseFlags_PublisherWithoutKeystore(t *testing.T) {
	_, _, err := parseFlags([]string{"--mode=publisher"})
	if err == nil {
		t.Fatal("expected error: publisher needs keystore")
	}
}

func TestRun_DevResolverStartsAndStops(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := run(ctx, []string{"--mode=resolver", "--dev"})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("dev resolver run: %v", err)
	}
}

func TestRun_DevPublisherStartsAndStops(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := run(ctx, []string{"--mode=publisher", "--dev"})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("dev publisher run: %v", err)
	}
}

// TestSeedOverlayCache_StaticOverlayOnly_NoChain validates the
// static-overlay-only example's promise: with no chain entry but an
// enabled overlay carrying pins, the seed loop populates the cache so
// ListKnown returns the address with no manual Refresh.
func TestSeedOverlayCache_StaticOverlayOnly_NoChain(t *testing.T) {
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	yaml := fmt.Sprintf(`overlay:
  - eth_address: "%s"
    enabled: true
    unsigned_allowed: true
    pin:
      - id: tx-1
        url: "https://tx-1.example.com:8935"
        capabilities:
          - name: "livepeer:transcoder/h264"
            work_unit: frame
`, addr)
	o, err := config.ParseOverlayYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("parse overlay: %v", err)
	}

	r, cacheRepo := newOverlayResolverFixture(t, chain.NewInMemory(types.EthAddress("0x0")), o)
	seedOverlayCache(context.Background(), r, o, logger.Discard())

	addrs, err := cacheRepo.List()
	if err != nil {
		t.Fatalf("list cache: %v", err)
	}
	if len(addrs) != 1 || addrs[0] != addr {
		t.Fatalf("expected cache to contain %s, got %v", addr, addrs)
	}
}

// TestSeedOverlayCache_OverlayOnlyWithChain validates the production
// overlay-only path: chain has serviceURIs (no manifest published yet),
// the seed loop walks the overlay and falls into legacy synth per
// address. ListKnown reflects every overlay entry after seed completes.
func TestSeedOverlayCache_OverlayOnlyWithChain(t *testing.T) {
	addrA, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	addrB, _ := types.ParseEthAddress("0xfedcba0000000000000000000000000000000000")
	yaml := fmt.Sprintf(`overlay:
  - eth_address: "%s"
    enabled: true
    unsigned_allowed: true
  - eth_address: "%s"
    enabled: true
    unsigned_allowed: true
`, addrA, addrB)
	o, err := config.ParseOverlayYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("parse overlay: %v", err)
	}

	c := chain.NewInMemory(types.EthAddress("0x0"))
	c.PreLoad(addrA, "https://a.example.com:8935")
	c.PreLoad(addrB, "https://b.example.com:8935")

	r, cacheRepo := newOverlayResolverFixture(t, c, o)
	seedOverlayCache(context.Background(), r, o, logger.Discard())

	addrs, err := cacheRepo.List()
	if err != nil {
		t.Fatalf("list cache: %v", err)
	}
	if len(addrs) != 2 {
		t.Fatalf("expected 2 cached entries, got %d (%v)", len(addrs), addrs)
	}
}

// TestSeedOverlayCache_SkipsDisabledEntries asserts disabled overlay
// rows are not seeded — same posture as the chain-less synth path.
func TestSeedOverlayCache_SkipsDisabledEntries(t *testing.T) {
	addrA, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	addrB, _ := types.ParseEthAddress("0xfedcba0000000000000000000000000000000000")
	yaml := fmt.Sprintf(`overlay:
  - eth_address: "%s"
    enabled: true
    unsigned_allowed: true
    pin:
      - id: tx-1
        url: "https://tx-1.example.com:8935"
        capabilities:
          - name: "livepeer:transcoder/h264"
            work_unit: frame
  - eth_address: "%s"
    enabled: false
    pin:
      - id: tx-2
        url: "https://tx-2.example.com:8935"
        capabilities:
          - name: "livepeer:transcoder/h264"
            work_unit: frame
`, addrA, addrB)
	o, err := config.ParseOverlayYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("parse overlay: %v", err)
	}

	r, cacheRepo := newOverlayResolverFixture(t, chain.NewInMemory(types.EthAddress("0x0")), o)
	seedOverlayCache(context.Background(), r, o, logger.Discard())

	addrs, err := cacheRepo.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 1 || addrs[0] != addrA {
		t.Fatalf("expected only %s in cache, got %v", addrA, addrs)
	}
}

// newOverlayResolverFixture builds a minimal resolver wired against the
// supplied chain + overlay. The overlay accessor is fixed to o so tests
// don't need to thread a separate atomic pointer through.
func newOverlayResolverFixture(t *testing.T, c chain.Chain, o *config.Overlay) (*resolver.Service, manifestcache.Repo) {
	t.Helper()
	kv := store.NewMemory()
	cacheRepo := manifestcache.New(kv)
	r := resolver.New(resolver.Config{
		Chain:    c,
		Fetcher:  &manifestfetcher.Static{Bodies: map[string][]byte{}},
		Verifier: verifier.New(),
		Cache:    cacheRepo,
		Audit:    audit.New(kv),
		Overlay:  func() *config.Overlay { return o },
		Clock:    &clock.Fixed{T: time.Unix(1745000000, 0).UTC()},
	})
	return r, cacheRepo
}
