package publisher

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/signer"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/repo/audit"
)

func newPublisher(t *testing.T) (*Service, signer.Signer, *chain.InMemory) {
	t.Helper()
	sk, err := signer.GenerateRandom()
	if err != nil {
		t.Fatal(err)
	}
	c := chain.NewInMemory(sk.Address())
	a := audit.New(store.NewMemory())
	clk := &clock.Fixed{T: time.Unix(1745000000, 0).UTC()}
	return New(Config{Chain: c, Signer: sk, Audit: a, Clock: clk}), sk, c
}

func TestGetIdentity_ReturnsSignerAddress(t *testing.T) {
	p, sk, _ := newPublisher(t)
	got, err := p.Identity()
	if err != nil {
		t.Fatal(err)
	}
	if got != sk.Address() {
		t.Fatalf("Identity = %s, want %s", got, sk.Address())
	}
}
