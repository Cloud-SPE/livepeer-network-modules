package inmemory

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestNewNilKey(t *testing.T) {
	_, err := New(nil)
	if !errors.Is(err, ErrNilKey) {
		t.Fatalf("want ErrNilKey, got %v", err)
	}
}

func TestAddressDerivation(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	ks, err := New(key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := crypto.PubkeyToAddress(key.PublicKey)
	got := ks.Address()
	if len(got) != 20 {
		t.Fatalf("Address() length = %d, want 20", len(got))
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Errorf("Address = %x, want %x", got, want.Bytes())
	}
}

func TestAddressReturnsCopy(t *testing.T) {
	key, _ := crypto.GenerateKey()
	ks, _ := New(key)
	a := ks.Address()
	a[0] ^= 0xff
	b := ks.Address()
	if bytes.Equal(a, b) {
		t.Error("Address() must return a defensive copy; mutation leaked")
	}
}

func TestSignProducesPersonalSignSignature(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	ks, err := New(key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	msg := []byte("canonical ticket hash")
	sig, err := ks.Sign(msg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(sig) != 65 {
		t.Fatalf("sig length %d, want 65", len(sig))
	}
	// EIP-191 personal_sign requires V ∈ {27, 28}.
	if sig[64] != 27 && sig[64] != 28 {
		t.Fatalf("sig V = %d, want 27 or 28", sig[64])
	}
}

func TestSignSignatureRecoversToLoadedAddress(t *testing.T) {
	key, _ := crypto.GenerateKey()
	ks, _ := New(key)

	msg := []byte("ticket bytes go here")
	sig, err := ks.Sign(msg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Recover: shift V back to {0,1}, hash with TextHash, recover pubkey.
	normalized := append([]byte(nil), sig...)
	normalized[64] -= 27
	digest := accounts.TextHash(msg)
	pub, err := crypto.SigToPub(digest, normalized)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	got := crypto.PubkeyToAddress(*pub)
	if !bytes.Equal(got.Bytes(), ks.Address()) {
		t.Errorf("recovered addr = %s, want %x", got.Hex(), ks.Address())
	}
}

func TestSignDeterministic(t *testing.T) {
	// crypto.Sign is deterministic (RFC6979) for a given (key, digest).
	key, _ := crypto.GenerateKey()
	ks, _ := New(key)
	msg := []byte("determinism")
	a, err := ks.Sign(msg)
	if err != nil {
		t.Fatalf("sign a: %v", err)
	}
	b, err := ks.Sign(msg)
	if err != nil {
		t.Fatalf("sign b: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("sig differs: %x vs %x", a, b)
	}
}

func TestStringRedactsKey(t *testing.T) {
	key, _ := crypto.GenerateKey()
	ks, _ := New(key)
	s := ks.String()
	if !strings.Contains(s, "redacted") {
		t.Errorf("String() = %q, expected 'redacted' marker", s)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)
	if !strings.Contains(s, addr.Hex()) {
		t.Errorf("String() should include address: %q", s)
	}
}

func TestSatisfiesProvidersKeyStoreInterface(t *testing.T) {
	// Compile-time assertion via type assertion at runtime — the import
	// cycle if we tried to import providers from here forces this shape.
	// The interface is small enough that asserting method signatures by
	// duck-typing is sufficient.
	var ks any
	key, _ := crypto.GenerateKey()
	k, _ := New(key)
	ks = k
	_, hasAddress := ks.(interface{ Address() []byte })
	_, hasSign := ks.(interface {
		Sign(hash []byte) ([]byte, error)
	})
	if !hasAddress || !hasSign {
		t.Fatalf("KeyStore missing providers.KeyStore methods (Address=%v, Sign=%v)", hasAddress, hasSign)
	}
}
