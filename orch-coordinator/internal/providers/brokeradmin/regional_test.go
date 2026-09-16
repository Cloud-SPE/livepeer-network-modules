package brokeradmin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRegionalCredentialRotationAndTLS(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	token := strings.Repeat("a", 64)
	if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	var got string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Livepeer-Pool-ID") != "pool_us" {
			t.Error("missing pool scope")
		}
		got = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"runners":[]}`))
	}))
	defer srv.Close()
	c := New(time.Second, []Target{{Name: "audio", BaseURL: srv.URL, PoolID: "pool_us", TokenFile: tokenFile}})
	if !c.Administrable("audio") {
		t.Fatal("token file not recognized")
	}
	if _, err := c.Runners(context.Background(), "audio"); err == nil {
		t.Fatal("untrusted server certificate accepted")
	}
	c.HTTP.Transport = srv.Client().Transport
	if _, err := c.Runners(context.Background(), "audio"); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer "+token {
		t.Fatal("missing scoped token")
	}
	token = strings.Repeat("b", 64)
	if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Runners(context.Background(), "audio"); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer "+token {
		t.Fatal("rotation not loaded")
	}
	target := c.targets["audio"]
	target.BaseURL = "http://example.invalid"
	c.targets["audio"] = target
	if _, err := c.Runners(context.Background(), "audio"); err == nil {
		t.Fatal("plaintext accepted")
	}
}
