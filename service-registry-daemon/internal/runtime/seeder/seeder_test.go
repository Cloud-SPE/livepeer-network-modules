package seeder

import (
	"context"
	"errors"
	cc "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/roundclock"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/repo/manifestcache"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/resolver"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
	"testing"
)

type testClock struct {
	roundclock.NamedClock
	events chan cc.Round
	err    error
}

func (c testClock) SubscribeRoundsForName(context.Context, string) (<-chan cc.Round, error) {
	return c.events, c.err
}

type testDiscovery struct {
	err   error
	calls int
}

func (d *testDiscovery) ActiveOrchs(context.Context) ([]types.EthAddress, error) {
	d.calls++
	return []types.EthAddress{"0xabcdef0000000000000000000000000000000000"}, d.err
}
func TestSeederRoundAndFailures(t *testing.T) {
	r := resolver.New(resolver.Config{Chain: chain.NewInMemory(""), Cache: manifestcache.New(store.NewMemory())})
	for _, kind := range []string{"round", "discovery failure", "subscription failure", "cancel", "stopped"} {
		t.Run(kind, func(t *testing.T) {
			d := &testDiscovery{}
			c := testClock{events: make(chan cc.Round, 1)}
			c.events <- cc.Round{}
			close(c.events)
			if kind == "discovery failure" {
				d.err = errors.New("offline")
			}
			if kind == "subscription failure" {
				c.err = errors.New("subscription")
			}
			s, err := New(Config{Discovery: d, Resolver: r, Clock: c})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancel" {
				cancel()
				c.events = nil
				s.clock = c
			}
			if kind == "stopped" {
				s.stopped = true
			}
			err = s.Run(ctx)
			wantErr := kind == "subscription failure" || kind == "stopped"
			if (err != nil) != wantErr {
				t.Fatalf("got %v", err)
			}
			if (kind == "round" || kind == "discovery failure") && d.calls != 1 {
				t.Fatalf("round not processed: %d", d.calls)
			}
		})
	}
	for _, cfg := range []Config{{}, {Discovery: &testDiscovery{}}, {Discovery: &testDiscovery{}, Resolver: r}} {
		if _, err := New(cfg); err == nil {
			t.Fatal("missing dependency accepted")
		}
	}
}
