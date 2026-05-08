package v3json_test

import (
	"crypto/ecdsa"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	ksiface "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/keystore"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/keystore/v3json"
	ethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
)

// writeKeystoreFile creates a real V3 JSON keystore file with the given
// password and returns its path + the address. Used by every test below.
func writeKeystoreFile(t *testing.T, password string) (string, chain.Address, *ecdsa.PrivateKey) {
	t.Helper()
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey)

	key := &ethkeystore.Key{
		Id:         uuid.New(),
		Address:    addr,
		PrivateKey: priv,
	}
	enc, err := ethkeystore.EncryptKey(key, password, ethkeystore.LightScryptN, ethkeystore.LightScryptP)
	if err != nil {
		t.Fatalf("EncryptKey: %v", err)
	}

	path := filepath.Join(t.TempDir(), "keystore.json")
	if err := os.WriteFile(path, enc, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path, addr, priv
}

func TestOpen_Success(t *testing.T) {
	path, addr, _ := writeKeystoreFile(t, "test-password")

	ks, err := v3json.Open(path, "test-password", chain.Address{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if ks.Address() != addr {
		t.Errorf("Address = %v, want %v", ks.Address(), addr)
	}
}

func TestOpen_ExpectedAddressMatch(t *testing.T) {
	path, addr, _ := writeKeystoreFile(t, "pw")

	ks, err := v3json.Open(path, "pw", addr)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if ks.Address() != addr {
		t.Errorf("Address mismatch")
	}
}

func TestOpen_ExpectedAddressMismatch(t *testing.T) {
	path, _, _ := writeKeystoreFile(t, "pw")
	wrong := chain.Address{0xff}

	_, err := v3json.Open(path, "pw", wrong)
	if err == nil || !strings.Contains(err.Error(), "does not match expected") {
		t.Errorf("Open with wrong expected = %v, want mismatch error", err)
	}
}

func TestOpen_MissingPath(t *testing.T) {
	if _, err := v3json.Open("", "pw", chain.Address{}); err == nil {
		t.Errorf("Open(\"\") should fail")
	}
}

func TestOpen_FileNotFound(t *testing.T) {
	if _, err := v3json.Open("/no/such/file.json", "pw", chain.Address{}); err == nil {
		t.Errorf("Open(missing) should fail")
	}
}

func TestOpen_BadPassword(t *testing.T) {
	path, _, _ := writeKeystoreFile(t, "correct")
	_, err := v3json.Open(path, "wrong", chain.Address{})
	if err == nil || !strings.Contains(err.Error(), "decrypt") {
		t.Errorf("Open with bad password = %v, want decrypt err", err)
	}
}

func TestRawSign_VerifiableNoPrefix(t *testing.T) {
	path, addr, _ := writeKeystoreFile(t, "pw")
	ks, err := v3json.Open(path, "pw", addr)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	rs, ok := ks.(ksiface.RawSigner)
	if !ok {
		t.Fatalf("v3json keystore should satisfy RawSigner")
	}

	payload := []byte("ticket-hash-bytes")
	sig, err := rs.RawSign(payload)
	if err != nil {
		t.Fatalf("RawSign: %v", err)
	}
	if len(sig) != 65 {
		t.Errorf("sig len = %d, want 65", len(sig))
	}

	// Recover the signer from the raw keccak256(payload), no EIP-191 prefix.
	hash := crypto.Keccak256(payload)
	pubkey, err := crypto.SigToPub(hash, sig)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	recovered := crypto.PubkeyToAddress(*pubkey)
	if recovered != addr {
		t.Errorf("recovered = %v, want %v", recovered, addr)
	}
}

func TestRawSignDiffersFromSign(t *testing.T) {
	path, addr, _ := writeKeystoreFile(t, "pw")
	ks, _ := v3json.Open(path, "pw", addr)

	rs := ks.(ksiface.RawSigner)
	payload := []byte("payload")

	sigPersonal, err := ks.Sign(payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	sigRaw, err := rs.RawSign(payload)
	if err != nil {
		t.Fatalf("RawSign: %v", err)
	}
	if string(sigPersonal) == string(sigRaw) {
		t.Errorf("Sign and RawSign should produce different signatures (different hash inputs)")
	}
}

func TestSign_Verifiable(t *testing.T) {
	path, addr, priv := writeKeystoreFile(t, "pw")
	ks, err := v3json.Open(path, "pw", addr)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	payload := []byte("hello world")
	sig, err := ks.Sign(payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(sig) != 65 {
		t.Errorf("sig len = %d, want 65", len(sig))
	}

	// Recover the signer and verify it matches our address.
	prefix := []byte("\x19Ethereum Signed Message:\n11")
	hash := crypto.Keccak256(prefix, payload)
	pubkey, err := crypto.SigToPub(hash, sig)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	recovered := crypto.PubkeyToAddress(*pubkey)
	if recovered != addr {
		t.Errorf("recovered = %v, want %v", recovered, addr)
	}

	// Sanity: the recovered pubkey matches the original priv.
	if recovered != crypto.PubkeyToAddress(priv.PublicKey) {
		t.Errorf("recovered does not match keystore's private key")
	}
}

func TestSignTx_Verifiable(t *testing.T) {
	path, addr, _ := writeKeystoreFile(t, "pw")
	ks, err := v3json.Open(path, "pw", addr)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	chainID := chain.ChainID(42161)
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID.BigInt(),
		Nonce:     1,
		GasTipCap: big.NewInt(1_000_000_000),
		GasFeeCap: big.NewInt(3_000_000_000),
		Gas:       21000,
		To:        &chain.Address{0x42},
		Value:     big.NewInt(0),
	})
	signed, err := ks.SignTx(tx, chainID)
	if err != nil {
		t.Fatalf("SignTx: %v", err)
	}

	// Recover the from-address from the signed tx.
	signer := types.LatestSignerForChainID(chainID.BigInt())
	from, err := types.Sender(signer, signed)
	if err != nil {
		t.Fatalf("Sender: %v", err)
	}
	if from != addr {
		t.Errorf("recovered sender = %v, want %v", from, addr)
	}
}

