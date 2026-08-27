package publisher

import (
	"errors"
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

func decodeSig(s string) ([]byte, error) {
	out := make([]byte, 65)
	if len(s) != 132 || s[:2] != "0x" {
		return nil, errors.New("malformed sig")
	}
	for i := 0; i < 65; i++ {
		hi, ok := nibble(s[2+i*2])
		lo, ok2 := nibble(s[2+i*2+1])
		if !ok || !ok2 {
			return nil, errors.New("non-hex")
		}
		out[i] = (hi << 4) | lo
	}
	return out, nil
}

func nibble(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
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
