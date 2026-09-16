package portal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberreport"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestReportCacheUnavailableIsNeverZeroOrAnotherWallet(t *testing.T) {
	dir := t.TempDir()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	raw, _ := json.Marshal(memberauth.PrivateKeyFile{Issuer: "https://portal.example", KeyID: "key", PrivateKey: base64.StdEncoding.EncodeToString(key)})
	signerPath := filepath.Join(dir, "issuer.json")
	os.WriteFile(signerPath, raw, 0600)
	walletKey, _ := crypto.GenerateKey()
	wallet := strings.ToLower(crypto.PubkeyToAddress(walletKey.PublicKey).Hex())
	var mode atomic.Int32
	regional := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode.Load() == 1 {
			http.Error(w, "offline", 503)
			return
		}
		report := memberreport.Report{PoolID: "eu", Wallet: wallet, ObservedAt: time.Now(), Windows: []memberreport.Window{}}
		if mode.Load() == 2 {
			report.Wallet = "other-wallet"
		}
		respond(w, 200, report)
	}))
	defer regional.Close()
	ca := filepath.Join(dir, "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: regional.Certificate().Raw}), 0600)
	client, err := NewRegionalClient(Region{PoolID: "eu", Name: "EU", URL: regional.URL, CAFile: ca}, signerPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(filepath.Join(dir, "portal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	challenge, _ := store.Challenge("https://portal.example", wallet, "192.0.2.1", now)
	sig, _ := crypto.Sign(accounts.TextHash([]byte(challenge.Message)), walletKey)
	secret, _, err := store.Login(challenge.ID, hex.EncodeToString(sig), now)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{Store: store, Origin: "https://portal.example", Regions: map[string]*RegionalClient{"eu": client}}
	handler, _ := server.Handler()
	request := func() string {
		r := httptest.NewRequest("GET", "https://portal.example/api/reports", nil)
		r.AddCookie(&http.Cookie{Name: cookieName, Value: secret})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	mode.Store(1)
	if body := request(); !strings.Contains(body, `"status":"unavailable"`) || strings.Contains(body, `"report":`) {
		t.Fatal(body)
	}
	mode.Store(0)
	if body := request(); !strings.Contains(body, `"status":"fresh"`) {
		t.Fatal(body)
	}
	mode.Store(1)
	if body := request(); !strings.Contains(body, `"status":"stale"`) || !strings.Contains(body, `"fetched_at":`) {
		t.Fatal(body)
	}
	mode.Store(2)
	if body := request(); strings.Contains(body, "other-wallet") || !strings.Contains(body, `"status":"stale"`) {
		t.Fatal(body)
	}
	if _, ok := server.cache.get("other-wallet/eu"); ok {
		t.Fatal("cache shared across wallets")
	}
}
