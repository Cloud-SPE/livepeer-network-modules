package chaintesting

import (
	"errors"
	"math/big"
	"testing"

	ksiface "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/keystore"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestFakeKeystore_DeterministicAddress(t *testing.T) {
	a := NewFakeKeystore("test-seed-A")
	b := NewFakeKeystore("test-seed-A")
	if a.Address() != b.Address() {
		t.Errorf("same seed should produce same address: %v vs %v", a.Address(), b.Address())
	}
	c := NewFakeKeystore("test-seed-B")
	if a.Address() == c.Address() {
		t.Errorf("different seeds should produce different addresses")
	}
}

func TestFakeKeystore_Sign(t *testing.T) {
	k := NewFakeKeystore("test")
	sig, err := k.Sign([]byte("hello"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(sig) != 65 {
		t.Errorf("Sign returned %d bytes, want 65 (secp256k1)", len(sig))
	}
	if k.SignCount() != 1 {
		t.Errorf("SignCount = %d, want 1", k.SignCount())
	}
}

func TestFakeKeystore_SignTx(t *testing.T) {
	k := NewFakeKeystore("test")
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(42161),
		Nonce:     0,
		GasTipCap: big.NewInt(1_000_000_000),
		GasFeeCap: big.NewInt(3_000_000_000),
		Gas:       21000,
	})
	signed, err := k.SignTx(tx, 42161)
	if err != nil {
		t.Fatalf("SignTx: %v", err)
	}
	v, r, s := signed.RawSignatureValues()
	if v == nil || r == nil || s == nil {
		t.Errorf("signed tx should have v, r, s set")
	}
}

func TestFakeKeystore_FailNextSign(t *testing.T) {
	k := NewFakeKeystore("test")
	want := errors.New("kaboom")
	k.FailNextSign(want)
	_, err := k.Sign([]byte("x"))
	if err != want {
		t.Errorf("Sign after FailNextSign = %v, want %v", err, want)
	}
	// Subsequent call recovers.
	if _, err := k.Sign([]byte("x")); err != nil {
		t.Errorf("Second Sign should succeed, got %v", err)
	}
}

func TestFakeKeystore_RawSign(t *testing.T) {
	k := NewFakeKeystore("test-seed")

	rs, ok := any(k).(ksiface.RawSigner)
	if !ok {
		t.Fatalf("FakeKeystore should satisfy RawSigner")
	}

	payload := []byte("ticket-payload")
	sig, err := rs.RawSign(payload)
	if err != nil {
		t.Fatalf("RawSign: %v", err)
	}
	if len(sig) != 65 {
		t.Errorf("sig len = %d, want 65", len(sig))
	}

	// Recover the signer from keccak256(payload) — no EIP-191 prefix.
	hash := crypto.Keccak256(payload)
	pubkey, err := crypto.SigToPub(hash, sig)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	recovered := crypto.PubkeyToAddress(*pubkey)
	if recovered != k.Address() {
		t.Errorf("recovered = %v, want %v", recovered, k.Address())
	}
}

func TestFakeKeystore_RawSignDiffersFromSign(t *testing.T) {
	k := NewFakeKeystore("test")
	rs := any(k).(ksiface.RawSigner)
	payload := []byte("payload")

	sigPersonal, _ := k.Sign(payload)
	sigRaw, _ := rs.RawSign(payload)
	if string(sigPersonal) == string(sigRaw) {
		t.Errorf("Sign and RawSign should produce different signatures")
	}
}

func TestFakeKeystore_RawSign_FailNextHonored(t *testing.T) {
	k := NewFakeKeystore("test")
	rs := any(k).(ksiface.RawSigner)
	want := errors.New("hsm down")
	k.FailNextSign(want)
	if _, err := rs.RawSign([]byte("x")); err != want {
		t.Errorf("RawSign with FailNextSign = %v, want %v", err, want)
	}
	// Recovers on next call.
	if _, err := rs.RawSign([]byte("x")); err != nil {
		t.Errorf("Second RawSign should succeed, got %v", err)
	}
}

func TestFakeKeystore_RawSign_DeterministicByAddress(t *testing.T) {
	a := NewFakeKeystore("seed")
	b := NewFakeKeystore("seed")
	rsa := any(a).(ksiface.RawSigner)
	rsb := any(b).(ksiface.RawSigner)

	payload := []byte("identical-payload")
	sigA, _ := rsa.RawSign(payload)
	sigB, _ := rsb.RawSign(payload)

	// Same seed → same key → same signature on same payload.
	if string(sigA) != string(sigB) {
		t.Errorf("RawSign should be deterministic across same-seed keystores")
	}
}
