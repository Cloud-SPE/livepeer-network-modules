package verifier

import (
	"crypto/ecdsa"
	"errors"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// roundTripSign is a tiny in-package signer to avoid a circular import
// with providers/signer.
func roundTripSign(t *testing.T, canonical []byte) (types.EthAddress, []byte, *ecdsa.PrivateKey) {
	t.Helper()
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addrHex := crypto.PubkeyToAddress(priv.PublicKey).Hex()
	addr, err := types.ParseEthAddress(addrHex)
	if err != nil {
		t.Fatal(err)
	}
	prefix := []byte("\x19Ethereum Signed Message:\n" + decimal(len(canonical)))
	digest := crypto.Keccak256(prefix, canonical)
	sig, err := crypto.Sign(digest, priv)
	if err != nil {
		t.Fatal(err)
	}
	if sig[64] < 27 {
		sig[64] += 27
	}
	return addr, sig, priv
}

func TestSecp256k1_Recover_HappyPath(t *testing.T) {
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

func TestSecp256k1_Recover_MalformedSig(t *testing.T) {
	v := New()
	_, err := v.Recover([]byte("x"), []byte{0x00})
	if !errors.Is(err, types.ErrSignatureMalformed) {
		t.Fatalf("expected ErrSignatureMalformed, got %v", err)
	}
}

func TestSecp256k1_Recover_TamperedCanonical(t *testing.T) {
	canonical := []byte("hello world")
	signerAddr, sig, _ := roundTripSign(t, canonical)
	v := New()
	tampered, err := v.Recover([]byte("HELLO world"), sig)
	if err != nil {
		// Recovery generally succeeds, just to a different address.
		// The "fail" path is acceptable too — we only require: not the original signer.
		return
	}
	if tampered.Equal(signerAddr) {
		t.Fatalf("tampered canonical recovered to original signer address")
	}
}

// decimal is a tiny zero-allocation int→string for a positive int. We
// use it so the test doesn't pull in fmt for a hot path.
func decimal(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 20)
	if n < 0 {
		buf = append(buf, '-')
		n = -n
	}
	start := len(buf)
	for n > 0 {
		buf = append(buf, byte('0'+(n%10)))
		n /= 10
	}
	// reverse digits in place
	for i, j := start, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
