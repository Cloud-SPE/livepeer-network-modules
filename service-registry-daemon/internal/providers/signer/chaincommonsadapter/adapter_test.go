package chaincommonsadapter_test

import (
	"strings"
	"testing"

	chaintest "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/signer"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/signer/chaincommonsadapter"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestNew_RequiresKeystore(t *testing.T) {
	if _, err := chaincommonsadapter.New(nil); err == nil {
		t.Errorf("New(nil) should fail")
	}
}

func TestNew_ProducesAdapter(t *testing.T) {
	ks := chaintest.NewFakeKeystore("test-orch")
	a, err := chaincommonsadapter.New(ks)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a == nil {
		t.Fatalf("New returned nil adapter")
	}
}

func TestAddress_LowerCaseCanonical(t *testing.T) {
	ks := chaintest.NewFakeKeystore("any-seed")
	a, _ := chaincommonsadapter.New(ks)

	got := string(a.Address())
	if !strings.HasPrefix(got, "0x") {
		t.Errorf("Address should be 0x-prefixed, got %q", got)
	}
	if got != strings.ToLower(got) {
		t.Errorf("Address should be lower-case, got %q", got)
	}
	// Sanity: the underlying keystore's checksummed Hex matches when lowered.
	if got != strings.ToLower(ks.Address().Hex()) {
		t.Errorf("Address mismatch: %q vs %q", got, strings.ToLower(ks.Address().Hex()))
	}
}

func TestSignCanonical_EIP191Recoverable(t *testing.T) {
	ks := chaintest.NewFakeKeystore("registry-orch")
	a, _ := chaincommonsadapter.New(ks)

	canonical := []byte("manifest canonical bytes here")
	sig, err := a.SignCanonical(canonical)
	if err != nil {
		t.Fatalf("SignCanonical: %v", err)
	}
	if len(sig) != 65 {
		t.Errorf("sig len = %d, want 65", len(sig))
	}

	// Recover via EIP-191: keccak256("\x19Ethereum Signed Message:\n<len>" || canonical)
	prefix := []byte("\x19Ethereum Signed Message:\n29")
	hash := crypto.Keccak256(prefix, canonical)
	pubkey, err := crypto.SigToPub(hash, sig)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	recovered := strings.ToLower(crypto.PubkeyToAddress(*pubkey).Hex())
	if recovered != string(a.Address()) {
		t.Errorf("recovered = %q, want %q", recovered, a.Address())
	}
}

func TestSignCanonical_DeterministicSameSeed(t *testing.T) {
	a, _ := chaincommonsadapter.New(chaintest.NewFakeKeystore("seed"))
	b, _ := chaincommonsadapter.New(chaintest.NewFakeKeystore("seed"))

	payload := []byte("identical-canonical")
	sigA, _ := a.SignCanonical(payload)
	sigB, _ := b.SignCanonical(payload)
	if string(sigA) != string(sigB) {
		t.Errorf("same-seed FakeKeystores should produce identical EIP-191 sigs on same payload")
	}
}

func TestSignerInterface_SatisfactionAtCompileTime(t *testing.T) {
	// Compile-time confirmation that the package's New returns a signer.Signer.
	var _ signer.Signer
	ks := chaintest.NewFakeKeystore("seed")
	got, err := chaincommonsadapter.New(ks)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var _ = got
}
