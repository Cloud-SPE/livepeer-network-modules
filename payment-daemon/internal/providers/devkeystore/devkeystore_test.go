package devkeystore

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestDefaultKeySignsAndReturnsCopies(t *testing.T) {
	k, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	addr := k.Address()
	addr[0] ^= 0xff
	if bytes.Equal(addr, k.Address()) {
		t.Fatal("Address did not return a copy")
	}
	msg := []byte("payment")
	sig, err := k.Sign(msg)
	if err != nil || len(sig) != 65 || (sig[64] != 27 && sig[64] != 28) {
		t.Fatalf("signature len=%d v=%d err=%v", len(sig), sig[64], err)
	}
	normalized := append([]byte(nil), sig...)
	normalized[64] -= 27
	pub, err := crypto.SigToPub(accounts.TextHash(msg), normalized)
	if err != nil || !bytes.Equal(crypto.PubkeyToAddress(*pub).Bytes(), k.Address()) {
		t.Fatalf("signature recovery err=%v", err)
	}
	if _, err := k.Sign(nil); err == nil {
		t.Fatal("empty hash accepted")
	}
}

func TestKeyOverrideValidation(t *testing.T) {
	if _, err := New("zz"); err == nil || !strings.Contains(err.Error(), "invalid hex") {
		t.Fatalf("invalid hex err=%v", err)
	}
	if _, err := New("01"); err == nil || !strings.Contains(err.Error(), "32 bytes") {
		t.Fatalf("short key err=%v", err)
	}
	if _, err := New(strings.Repeat("00", 32)); err == nil || !strings.Contains(err.Error(), "secp256k1") {
		t.Fatalf("zero key err=%v", err)
	}
	valid := strings.Repeat("01", 32)
	k, err := New(valid)
	if err != nil || len(k.Address()) != 20 {
		t.Fatalf("valid override key=%v err=%v", k, err)
	}
}
