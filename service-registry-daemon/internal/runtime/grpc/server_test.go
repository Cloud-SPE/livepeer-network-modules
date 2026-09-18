package grpc

import (
	"context"
	"errors"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/manifestfetcher"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/signer"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/verifier"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/repo/manifestcache"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/publisher"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/resolver"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

func TestNewServer_RequiresAtLeastOne(t *testing.T) {
	if _, err := NewServer(Config{}); err == nil {
		t.Fatal("expected error when neither service is provided")
	}
}

func TestServer_ResolverModeOnly_PublisherCallsRejected(t *testing.T) {
	kv := store.NewMemory()
	cache := manifestcache.New(kv)
	a := audit.New(kv)
	r := resolver.New(resolver.Config{
		Chain:    chain.NewInMemory(""),
		Fetcher:  &manifestfetcher.Static{},
		Verifier: verifier.New(),
		Cache:    cache,
		Audit:    a,
		Overlay:  func() *config.Overlay { return config.EmptyOverlay() },
		Clock:    clock.System{},
	})
	srv, err := NewServer(Config{Resolver: r, Cache: cache, Audit: a})
	if err != nil {
		t.Fatal(err)
	}
	if !srv.HasResolver() {
		t.Fatal("expected resolver mounted")
	}
	if srv.HasPublisher() {
		t.Fatal("publisher should not be mounted")
	}
}

func TestServer_BadAddressRejected(t *testing.T) {
	kv := store.NewMemory()
	r := resolver.New(resolver.Config{
		Chain:    chain.NewInMemory(""),
		Fetcher:  &manifestfetcher.Static{},
		Verifier: verifier.New(),
		Cache:    manifestcache.New(kv),
		Audit:    audit.New(kv),
		Overlay:  func() *config.Overlay { return config.EmptyOverlay() },
		Clock:    clock.System{},
	})
	srv, _ := NewServer(Config{Resolver: r, Cache: manifestcache.New(kv), Audit: audit.New(kv)})
	_, err := srv.ResolveByAddress(context.Background(), ResolveByAddressRequest{EthAddress: "not-an-address"})
	if !errors.Is(err, types.ErrInvalidEthAddress) {
		t.Fatalf("expected ErrInvalidEthAddress, got %v", err)
	}
}

func TestServer_HealthIncludesMode(t *testing.T) {
	sk, _ := signer.GenerateRandom()
	pub := publisher.New(publisher.Config{Chain: chain.NewInMemory(sk.Address()), Signer: sk, Audit: nil, Clock: clock.System{}})
	srv, _ := NewServer(Config{Publisher: pub})
	h := srv.Health(context.Background())
	if h.Mode != "publisher" {
		t.Fatalf("Mode = %s", h.Mode)
	}
}
