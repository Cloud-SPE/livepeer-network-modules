package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberauth"
)

func TestKeyGenerationProtectedExclusiveAndTrustVerifies(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "issuer.json")
	trust := filepath.Join(dir, "trust.json")
	if err := generateKey(key, trust, "https://portal.example", "initial"); err != nil {
		t.Fatal(err)
	}
	signer, err := memberauth.LoadSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Sign("0x"+strings.Repeat("1", 40), "eu", strings.Repeat("a", 64), time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (memberauth.Verifier{Path: trust, PoolID: "eu"}).Verify(token, time.Now()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(key)
	if err := generateKey(key, trust, "https://portal.example", "replacement"); err == nil {
		t.Fatal("key overwritten")
	}
	after, _ := os.ReadFile(key)
	if string(before) != string(after) {
		t.Fatal("existing key changed")
	}
	var pub memberauth.Trust
	raw, _ := os.ReadFile(trust)
	json.Unmarshal(raw, &pub)
	if len(pub.Keys) != 1 || strings.Contains(string(raw), "private_key") {
		t.Fatal("public trust contains private key")
	}
	another := filepath.Join(dir, "another.json")
	if err := generateKey(another, trust, "https://portal.example", "another"); err == nil {
		t.Fatal("trust overwritten")
	}
	if _, err := os.Stat(another); !os.IsNotExist(err) {
		t.Fatal("partial key retained on existing trust")
	}
	if err := generateKey(key, key, "https://portal.example", "initial"); err == nil {
		t.Fatal("same key/trust path accepted")
	}
}
