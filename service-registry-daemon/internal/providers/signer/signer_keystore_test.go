package signer

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestLoadKeystore_Roundtrip writes a fresh V3 keystore to a tempdir,
// then loads it and confirms the recovered address matches the
// generated key. Production parity test.
func TestLoadKeystore_Roundtrip(t *testing.T) {
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	wantAddr := crypto.PubkeyToAddress(priv.PublicKey)

	tmp := t.TempDir()
	ks := keystore.NewKeyStore(tmp, keystore.LightScryptN, keystore.LightScryptP)
	acct, err := ks.ImportECDSA(priv, "test-pw")
	if err != nil {
		t.Fatal(err)
	}

	s, err := LoadKeystore(acct.URL.Path, "test-pw")
	if err != nil {
		t.Fatalf("LoadKeystore: %v", err)
	}
	if !common.IsHexAddress(string(s.Address())) {
		t.Fatalf("address shape: %s", s.Address())
	}
	loaded := common.HexToAddress(string(s.Address()))
	if loaded != wantAddr {
		t.Fatalf("addr mismatch: got %s, want %s", loaded, wantAddr)
	}
}

func TestLoadKeystore_BadPath(t *testing.T) {
	_, err := LoadKeystore(filepath.Join(t.TempDir(), "nope.json"), "x")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadKeystore_BadPassword(t *testing.T) {
	priv, _ := crypto.GenerateKey()
	tmp := t.TempDir()
	ks := keystore.NewKeyStore(tmp, keystore.LightScryptN, keystore.LightScryptP)
	acct, _ := ks.ImportECDSA(priv, "right-pw")

	_, err := LoadKeystore(acct.URL.Path, "wrong-pw")
	if err == nil {
		t.Fatal("expected error on bad password")
	}
}

func TestFromHexKey_BadHex(t *testing.T) {
	if _, err := FromHexKey("notahexkey"); err == nil {
		t.Fatal("expected error")
	}
}

func TestAddressFromHexKey_Helper(t *testing.T) {
	const known = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	addr, err := AddressFromHexKey(known)
	if err != nil {
		t.Fatal(err)
	}
	if addr != common.HexToAddress("0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266") {
		t.Fatalf("got %s", addr.Hex())
	}
	if _, err := AddressFromHexKey("notakey"); err == nil {
		t.Fatal("expected error")
	}
}

func TestStrip0x(t *testing.T) {
	cases := map[string]string{
		"0xab": "ab",
		"0Xab": "ab",
		"ab":   "ab",
		"":     "",
		"0":    "0",
	}
	for in, want := range cases {
		if got := strip0x(in); got != want {
			t.Fatalf("strip0x(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeystore_AddressEmptyForNoKey(t *testing.T) {
	k := &Keystore{}
	if k.Address() != "" {
		t.Fatal("uninitialized keystore should have empty address")
	}
}

func TestKeystore_SignWithoutKey(t *testing.T) {
	k := &Keystore{}
	if _, err := k.SignCanonical([]byte("x")); err == nil {
		t.Fatal("expected error")
	}
	// Use of errors.New rather than errors.Is — the function returns an
	// errors.New("..."). The point is the error is non-nil.
	_ = errors.New
}
