package publisher

import (
	"errors"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/signer"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/verifier"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
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

func TestBuildManifest_StampsRequiredFields(t *testing.T) {
	p, sk, _ := newPublisher(t)
	m, err := p.BuildManifest(BuildSpec{
		EthAddress: sk.Address(),
		Nodes:      []types.Node{{ID: "n1", URL: "https://x.test", Capabilities: []types.Capability{}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != types.SchemaVersion {
		t.Fatalf("schema = %s", m.SchemaVersion)
	}
	if m.EthAddress != string(sk.Address()) {
		t.Fatalf("eth = %s", m.EthAddress)
	}
	if m.IssuedAt.IsZero() {
		t.Fatal("issued_at not set")
	}
	if m.Signature.Alg != types.SignatureAlgEthPersonal {
		t.Fatalf("sig.Alg = %s", m.Signature.Alg)
	}
	if m.Signature.Value != "" {
		t.Fatalf("BuildManifest should not sign yet")
	}
}

func TestBuildManifest_RejectsEmptyNodes(t *testing.T) {
	p, sk, _ := newPublisher(t)
	if _, err := p.BuildManifest(BuildSpec{EthAddress: sk.Address()}); !errors.Is(err, types.ErrEmptyNodes) {
		t.Fatalf("expected ErrEmptyNodes, got %v", err)
	}
}

func TestBuildManifest_RejectsMismatchedEthAddress(t *testing.T) {
	p, _, _ := newPublisher(t)
	if _, err := p.BuildManifest(BuildSpec{
		EthAddress: "0xabcdef0000000000000000000000000000000000",
		Nodes:      []types.Node{{ID: "n1", URL: "https://x.test", Capabilities: []types.Capability{}}},
	}); !errors.Is(err, types.ErrInvalidEthAddress) {
		t.Fatalf("expected ErrInvalidEthAddress, got %v", err)
	}
}

func TestSignManifest_ProducesVerifiableSig(t *testing.T) {
	p, sk, _ := newPublisher(t)
	m, _ := p.BuildManifest(BuildSpec{
		EthAddress: sk.Address(),
		Nodes:      []types.Node{{ID: "n1", URL: "https://x.test", Capabilities: []types.Capability{}}},
	})
	signed, err := p.SignManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if signed.Signature.Value == "" {
		t.Fatal("signature not stamped")
	}
	canonical, _ := types.CanonicalBytes(signed)
	sig, err := decodeSig(signed.Signature.Value)
	if err != nil {
		t.Fatal(err)
	}
	v := verifier.New()
	rec, err := v.Recover(canonical, sig)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Equal(sk.Address()) {
		t.Fatalf("recovered %s != signer %s", rec, sk.Address())
	}
}

// decodeSig is duplicated locally to avoid importing service/resolver.
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

func TestBuildAndSign_MatchesTwoCallPath(t *testing.T) {
	p, sk, _ := newPublisher(t)
	spec := BuildSpec{
		EthAddress: sk.Address(),
		Nodes: []types.Node{
			{ID: "n1", URL: "https://x.test", Capabilities: []types.Capability{{Name: "transcode-h264"}}},
			{ID: "n2", URL: "https://y.test", Capabilities: []types.Capability{{Name: "asr-en"}}},
		},
	}

	// Two-call path.
	built, err := p.BuildManifest(spec)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	signedTwoCall, err := p.SignManifest(built)
	if err != nil {
		t.Fatalf("SignManifest: %v", err)
	}
	canonicalTwoCall, err := types.CanonicalBytes(signedTwoCall)
	if err != nil {
		t.Fatalf("CanonicalBytes (two-call): %v", err)
	}

	// One-call path.
	signedOneCall, err := p.BuildAndSign(spec)
	if err != nil {
		t.Fatalf("BuildAndSign: %v", err)
	}
	canonicalOneCall, err := types.CanonicalBytes(signedOneCall)
	if err != nil {
		t.Fatalf("CanonicalBytes (one-call): %v", err)
	}

	if string(canonicalTwoCall) != string(canonicalOneCall) {
		t.Fatalf("canonical bytes differ — BuildAndSign isn't byte-identical to BuildManifest+SignManifest.\n  two-call: %s\n  one-call: %s",
			canonicalTwoCall, canonicalOneCall)
	}
	if signedOneCall.Signature.Value != signedTwoCall.Signature.Value {
		t.Fatalf("signature values differ — sig two-call=%s one-call=%s",
			signedTwoCall.Signature.Value, signedOneCall.Signature.Value)
	}
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
