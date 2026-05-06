package verify

import (
	"crypto/ecdsa"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

// roundTripSign mints a fresh secp256k1 key, signs canonical bytes
// under the EIP-191 envelope, returns the address, signature, and key.
// In-package so the verifier test does not depend on the secure-orch-
// console signer module.
func roundTripSign(t *testing.T, canonical []byte) (EthAddress, []byte, *ecdsa.PrivateKey) {
	t.Helper()
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr, err := parseEthAddress(crypto.PubkeyToAddress(priv.PublicKey).Hex())
	if err != nil {
		t.Fatal(err)
	}
	digest := PersonalSignDigest(canonical)
	sig, err := crypto.Sign(digest, priv)
	if err != nil {
		t.Fatal(err)
	}
	if sig[64] < 27 {
		sig[64] += 27
	}
	return addr, sig, priv
}

func TestRecover_HappyPath(t *testing.T) {
	canonical := []byte("hello world")
	addr, sig, _ := roundTripSign(t, canonical)
	v := New()
	got, err := v.Recover(canonical, sig)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(addr) {
		t.Fatalf("recovered %s, want %s", got, addr)
	}
}

func TestRecover_MalformedSig(t *testing.T) {
	v := New()
	_, err := v.Recover([]byte("x"), []byte{0x00})
	if !errors.Is(err, ErrSignatureMalformed) {
		t.Fatalf("expected ErrSignatureMalformed, got %v", err)
	}
}

func TestRecover_TamperedCanonical(t *testing.T) {
	canonical := []byte("hello world")
	signerAddr, sig, _ := roundTripSign(t, canonical)
	v := New()
	tampered, err := v.Recover([]byte("HELLO world"), sig)
	if err != nil {
		// recover may fail outright; that's acceptable too.
		return
	}
	if tampered.Equal(signerAddr) {
		t.Fatalf("tampered canonical recovered to original signer address")
	}
}

func TestRecover_AgainstFixture(t *testing.T) {
	// Read the canonical fixture mirrored from
	// secure-orch-console/testdata/canonical/. Sign with a fresh key
	// and recover; the recovered address must equal the signing key's
	// address. This is the symmetric companion to the signer's
	// fixture round-trip test.
	canonical, err := os.ReadFile(filepath.Join("testdata", "canonical", "manifest-minimal.canonical.json"))
	if err != nil {
		t.Fatal(err)
	}
	addr, sig, _ := roundTripSign(t, canonical)
	got, err := New().Recover(canonical, sig)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !got.Equal(addr) {
		t.Fatalf("recovered %s != signer %s", got, addr)
	}
}

func TestPersonalSignDigest_Stable(t *testing.T) {
	d1 := PersonalSignDigest([]byte("hello"))
	d2 := PersonalSignDigest([]byte("hello"))
	if string(d1) != string(d2) {
		t.Fatal("digest unstable")
	}
	if len(d1) != 32 {
		t.Fatalf("expected 32-byte digest, got %d", len(d1))
	}
}

func TestEthAddress_LowerCases(t *testing.T) {
	a, err := parseEthAddress("0xABCDEF1234567890ABCDEF1234567890ABCDEF12")
	if err != nil {
		t.Fatal(err)
	}
	if a.String() != "0xabcdef1234567890abcdef1234567890abcdef12" {
		t.Fatalf("not lower-cased: %s", a)
	}
	if !strings.HasPrefix(a.String(), "0x") {
		t.Fatalf("missing 0x prefix: %s", a)
	}
}

func TestEthAddress_RejectsBadInput(t *testing.T) {
	cases := []string{"abcd", "0x12", "0xZZZ", ""}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if _, err := parseEthAddress(c); err == nil || !errors.Is(err, ErrInvalidEthAddress) {
				t.Fatalf("expected ErrInvalidEthAddress, got %v", err)
			}
		})
	}
}
