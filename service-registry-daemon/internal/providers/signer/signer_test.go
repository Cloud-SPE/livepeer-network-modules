package signer

import (
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/verifier"
)

func TestKeystore_GenerateAndSignVerify(t *testing.T) {
	s, err := GenerateRandom()
	if err != nil {
		t.Fatal(err)
	}
	canonical := []byte(`{"hello":"world"}`)
	sig, err := s.SignCanonical(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 65 {
		t.Fatalf("expected 65-byte sig, got %d", len(sig))
	}
	if sig[64] != 27 && sig[64] != 28 {
		t.Fatalf("expected v in {27,28}, got %d", sig[64])
	}

	v := verifier.New()
	recovered, err := v.Recover(canonical, sig)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !recovered.Equal(s.Address()) {
		t.Fatalf("recovered %s != signer %s", recovered, s.Address())
	}
}

func TestKeystore_FromHexKey(t *testing.T) {
	// Well-known test key. Address is deterministic.
	const hexKey = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	s, err := FromHexKey(hexKey)
	if err != nil {
		t.Fatal(err)
	}
	want := "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266"
	if !strings.EqualFold(string(s.Address()), want) {
		t.Fatalf("FromHexKey address = %s, want %s", s.Address(), want)
	}
}

func TestKeystore_NilSafe(t *testing.T) {
	var s *Keystore
	if s.Address() != "" {
		t.Fatal("nil signer should return empty address")
	}
	if _, err := s.SignCanonical([]byte("x")); err == nil {
		t.Fatal("nil signer should error on sign")
	}
	s.Close() // must not panic
}

func TestKeystore_CloseZeros(t *testing.T) {
	s, _ := GenerateRandom()
	s.Close()
	if _, err := s.SignCanonical([]byte("x")); err == nil {
		t.Fatal("closed signer should error")
	}
}

func TestPersonalSignDigest_StableShape(t *testing.T) {
	d1 := PersonalSignDigest([]byte("hello"))
	d2 := PersonalSignDigest([]byte("hello"))
	if string(d1) != string(d2) {
		t.Fatal("digest unstable")
	}
	if len(d1) != 32 {
		t.Fatalf("expected 32-byte digest, got %d", len(d1))
	}
}
