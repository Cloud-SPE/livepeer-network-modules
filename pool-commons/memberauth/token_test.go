package memberauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemberTokensAreLocallyPoolScopedAndRevocable(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer := &Signer{Issuer: "member-portal", KeyID: "key-1", PrivateKey: private}
	path := filepath.Join(t.TempDir(), "trust.json")
	trust := Trust{Issuer: signer.Issuer, Keys: []PublicKey{{ID: signer.KeyID, PublicKey: base64.StdEncoding.EncodeToString(public), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}}}
	save := func() {
		raw, _ := json.Marshal(trust)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save()
	session := strings.Repeat("a", 64)
	wallet := "0x" + strings.Repeat("1", 40)
	token, err := signer.Sign(wallet, "pool_eu", session, now, MaxTTL)
	if err != nil {
		t.Fatal(err)
	}
	verifier := Verifier{Path: path, PoolID: "pool_eu"}
	claims, err := verifier.Verify(token, now)
	if err != nil || claims.Wallet != wallet || claims.Scope != Scope {
		t.Fatalf("member proof %+v %v", claims, err)
	}
	wrong := verifier
	wrong.PoolID = "pool_us"
	if _, err := wrong.Verify(token, now); err == nil {
		t.Fatal("cross-region authorization")
	}
	if _, err := verifier.Verify(token, now.Add(MaxTTL)); err == nil {
		t.Fatal("expired token accepted")
	}
	// Even a valid signature cannot expand the fixed member scope or lifetime.
	for _, change := range []func(*Claims){func(c *Claims) { c.Scope = "pool-admin" }, func(c *Claims) { c.ExpiresAt += 1 }, func(c *Claims) { c.Version = 2 }, func(c *Claims) { c.Wallet = "not-a-wallet" }, func(c *Claims) { c.Issuer = "another-portal" }, func(c *Claims) { c.IssuedAt = now.Add(time.Minute).Unix(); c.ExpiresAt = c.IssuedAt + 60 }} {
		changed := claims
		change(&changed)
		raw, _ := json.Marshal(changed)
		payload := Prefix + base64.RawURLEncoding.EncodeToString(raw)
		signed := payload + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, []byte(payload)))
		if _, err := verifier.Verify(signed, now); err == nil {
			t.Fatalf("expanded claims accepted %+v", changed)
		}
	}
	parts := strings.Split(token, ".")
	changed := claims
	changed.Wallet = "0x" + strings.Repeat("2", 40)
	raw, _ := json.Marshal(changed)
	parts[1] = base64.RawURLEncoding.EncodeToString(raw)
	if _, err := verifier.Verify(strings.Join(parts, "."), now); err == nil {
		t.Fatal("wallet tampering accepted")
	}
	trust.RevokedSessions = []string{session}
	save()
	if _, err := verifier.Verify(token, now); err == nil {
		t.Fatal("revoked session accepted after trust reload")
	}
	trust.RevokedSessions = nil
	trust.Keys[0].Revoked = true
	save()
	if _, err := verifier.Verify(token, now); err == nil {
		t.Fatal("revoked signing key accepted")
	}
	trust.Keys[0].Revoked = false
	trust.Keys = append(trust.Keys, trust.Keys[0])
	save()
	if _, err := verifier.Verify(token, now); err == nil {
		t.Fatal("ambiguous trust accepted")
	}
}
func TestSignerKeyFileAndTokenLifetimeFailClosed(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "signer.json")
	raw, _ := json.Marshal(PrivateKeyFile{Issuer: "member-portal", KeyID: "key", PrivateKey: base64.StdEncoding.EncodeToString(private)})
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSigner(path); err == nil {
		t.Fatal("world-readable signer accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := LoadSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, ttl := range []time.Duration{0, MaxTTL + time.Second} {
		if _, err := signer.Sign("0x"+strings.Repeat("1", 40), "pool", strings.Repeat("a", 64), time.Now(), ttl); err == nil {
			t.Fatal("unsafe lifetime accepted")
		}
	}
}
