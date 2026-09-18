package portal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberauth"
)

func TestHTTPSRegionalScopeAndRedirectDenial(t *testing.T) {
	dir := t.TempDir()
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	signerPath := filepath.Join(dir, "issuer.json")
	trustPath := filepath.Join(dir, "trust.json")
	write := func(path string, value any, mode os.FileMode) {
		raw, _ := json.Marshal(value)
		if err := os.WriteFile(path, raw, mode); err != nil {
			t.Fatal(err)
		}
	}
	write(signerPath, memberauth.PrivateKeyFile{Issuer: "https://portal.example", KeyID: "one", PrivateKey: base64.StdEncoding.EncodeToString(private)}, 0600)
	trust := memberauth.Trust{Issuer: "https://portal.example", Keys: []memberauth.PublicKey{{ID: "one", PublicKey: base64.StdEncoding.EncodeToString(public), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}}}
	write(trustPath, trust, 0600)
	var seen atomic.Int32
	var redirect atomic.Bool
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen.Add(1); w.WriteHeader(200) }))
	defer other.Close()
	for _, pool := range []string{"eu-pool", "us-pool"} {
		t.Run(pool, func(t *testing.T) {
			var origin string
			regional := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Origin") != origin {
					t.Error("origin not pinned")
				}
				claims, err := (memberauth.Verifier{Path: trustPath, PoolID: pool}).Verify(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), time.Now())
				if err != nil {
					http.Error(w, "unauthorized", 401)
					return
				}
				if claims.Wallet != "0x"+strings.Repeat("1", 40) || claims.Scope != memberauth.Scope {
					t.Error("wrong member identity")
				}
				if _, err := (memberauth.Verifier{Path: trustPath, PoolID: "another-pool"}).Verify(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), time.Now()); err == nil {
					t.Error("cross pool accepted")
				}
				if redirect.Load() {
					http.Redirect(w, r, other.URL, 302)
					return
				}
				w.WriteHeader(200)
			}))
			defer regional.Close()
			origin = regional.URL
			ca := filepath.Join(dir, pool+".pem")
			os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: regional.Certificate().Raw}), 0600)
			client, err := NewRegionalClient(Region{PoolID: pool, Name: pool, URL: origin, CAFile: ca}, signerPath)
			if err != nil {
				t.Fatal(err)
			}
			session := Session{ID: strings.Repeat("a", 64), Wallet: "0x" + strings.Repeat("1", 40), ExpiresAt: now.Add(time.Hour)}
			response, err := client.Do(context.Background(), session, "GET", "/member/v1/membership", nil)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal(response.StatusCode)
			}
			redirect.Store(true)
			response, err = client.Do(context.Background(), session, "GET", "/member/v1/membership", nil)
			if response != nil {
				response.Body.Close()
			}
			if err == nil {
				t.Fatal("redirect followed")
			}
			redirect.Store(false)
			if _, err := client.Do(context.Background(), session, "POST", "/admin/v1/payouts", nil); err == nil {
				t.Fatal("admin operation allowed")
			}
			session.ExpiresAt = now.Add(-time.Second)
			if _, err := client.Do(context.Background(), session, "GET", "/member/v1/membership", nil); err == nil {
				t.Fatal("expired session issued token")
			}
		})
	}
	if seen.Load() != 0 {
		t.Fatal("authorization leaked to redirect")
	}
	if _, err := NewRegionalClient(Region{PoolID: "eu", Name: "EU", URL: "http://localhost"}, signerPath); err == nil {
		t.Fatal("plaintext accepted")
	}
}
