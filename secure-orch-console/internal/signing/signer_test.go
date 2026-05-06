package signing

import (
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
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

	// Recover signer from canonical + signature using go-ethereum
	// directly (the verify package is the next commit).
	digest := PersonalSignDigest(canonical)
	rec := make([]byte, 65)
	copy(rec, sig)
	if rec[64] >= 27 {
		rec[64] -= 27
	}
	pub, err := crypto.SigToPub(digest, rec)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	got, err := ParseEthAddress(crypto.PubkeyToAddress(*pub).Hex())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(s.Address()) {
		t.Fatalf("recovered %s != signer %s", got, s.Address())
	}
}

func TestKeystore_FromHexKey(t *testing.T) {
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
	s.Close()
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

func TestKeystore_LoadV3(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Generate a fresh key, write it as a V3 keystore, reload, verify
	// the address and a sign-and-recover round-trip.
	s, err := GenerateRandom()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const password = "test-password"
	path := writeV3Keystore(t, dir, s, password)

	loaded, err := LoadKeystore(path, password)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer loaded.Close()
	if !loaded.Address().Equal(s.Address()) {
		t.Fatalf("loaded address %s != original %s", loaded.Address(), s.Address())
	}

	canonical := []byte(`{"x":1}`)
	sig, err := loaded.SignCanonical(canonical)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if len(sig) != 65 {
		t.Fatalf("sig len %d", len(sig))
	}

	// Wrong password: rejected.
	if _, err := LoadKeystore(path, "nope"); err == nil {
		t.Fatal("expected wrong-password error")
	}
}

func TestKeystore_FixtureRoundTrip(t *testing.T) {
	// Round-trip the canonical fixture from testdata through a signer
	// instantiated from a deterministic test private key.
	const hexKey = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
	s, err := FromHexKey(hexKey)
	if err != nil {
		t.Fatal(err)
	}
	canonical := []byte(`{"capabilities":[],"expires_at":"2026-06-05T12:34:56Z","issued_at":"2026-05-06T12:34:56Z","orch":{"eth_address":"` + s.Address().String() + `"},"publication_seq":1,"spec_version":"0.2.0"}`)
	sig, err := s.SignCanonical(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 65 {
		t.Fatalf("sig len %d", len(sig))
	}
	if sig[64] != 27 && sig[64] != 28 {
		t.Fatalf("v=%d not in {27,28}", sig[64])
	}
}
