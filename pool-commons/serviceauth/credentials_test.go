package serviceauth

import (
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}
func TestCredentialScopeRotationAndRevocation(t *testing.T) {
	token, hash, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	c := Credential{ID: "us-reconciler", TokenSHA256: hash, PoolID: "pool_us", Roles: []string{"revenue-reader"}, Resources: []string{"audio-broker"}, ExpiresAt: now.Add(time.Hour)}
	path := filepath.Join(t.TempDir(), "credentials.json")
	writeFile(t, path, File{[]Credential{c}})
	verifier := Verifier{Path: path, Now: func() time.Time { return now }}
	req := httptest.NewRequest("GET", "https://audio-broker/revenue", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(PoolHeader, "pool_us")
	if _, err := verifier.Authorize(req, "pool_us", "audio-broker", "revenue-reader"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ pool, resource, role string }{{"pool_eu", "audio-broker", "revenue-reader"}, {"pool_us", "llm-broker", "revenue-reader"}, {"pool_us", "audio-broker", "pool-admin"}} {
		if _, err := verifier.Authorize(req, tc.pool, tc.resource, tc.role); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	c.Revoked = true
	writeFile(t, path, File{[]Credential{c}})
	if _, err := verifier.Authorize(req, "pool_us", "audio-broker", "revenue-reader"); err == nil {
		t.Fatal("revocation ignored")
	}
	c.Revoked = false
	c.ExpiresAt = now
	writeFile(t, path, File{[]Credential{c}})
	if _, err := verifier.Authorize(req, "pool_us", "audio-broker", "revenue-reader"); err == nil {
		t.Fatal("expired token accepted")
	}
	replacement, replacementHash, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	c.TokenSHA256 = replacementHash
	c.ExpiresAt = now.Add(time.Hour)
	writeFile(t, path, File{[]Credential{c}})
	if _, err := verifier.Authorize(req, "pool_us", "audio-broker", "revenue-reader"); err == nil {
		t.Fatal("old token survived rotation")
	}
	req.Header.Set("Authorization", "Bearer "+replacement)
	if _, err := verifier.Authorize(req, "pool_us", "audio-broker", "revenue-reader"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, File{[]Credential{c, c}})
	if _, err := verifier.Authorize(req, "pool_us", "audio-broker", "revenue-reader"); err == nil {
		t.Fatal("ambiguous credential file accepted")
	}
}

func TestHTTPSIdentityAndNoCredentialRedirect(t *testing.T) {
	token, _, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	var destinationCalls int
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls++; w.WriteHeader(200) }))
	defer destination.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get(PoolHeader) != "pool_us" {
			t.Error("missing scoped auth")
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, destination.URL, 302)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer source.Close()
	roots := x509.NewCertPool()
	roots.AddCert(source.Certificate())
	client, err := HTTPSClient(source.URL, "pool_us", tokenPath, roots)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if _, err := client.Get(source.URL + "/redirect"); err == nil {
		t.Fatal("followed redirect")
	}
	if _, err := client.Get(destination.URL); err == nil {
		t.Fatal("sent token to different origin")
	}
	if destinationCalls != 0 {
		t.Fatal("credential destination contacted")
	}
	untrusted, err := HTTPSClient(source.URL, "pool_us", tokenPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := untrusted.Get(source.URL); err == nil {
		t.Fatal("untrusted certificate accepted")
	}
	if _, err := HTTPSClient("http://host", "pool_us", tokenPath, nil); err == nil {
		t.Fatal("plaintext client accepted")
	}
}
