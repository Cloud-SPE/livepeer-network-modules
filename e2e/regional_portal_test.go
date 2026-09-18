package e2e

import (
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberauth"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
)

type regionalProcess struct {
	harness                     *pool
	id, data, config, memberURL string
	proxy                       *httptest.Server
	cmd                         *exec.Cmd
}

// This is TLS termination only; all member/auth/report responses come from the
// actual component process. Preserve Host and scheme as the deployment proxy does.
func tlsProcessProxy(t *testing.T, target string) *httptest.Server {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	reverse := httputil.NewSingleHostReverseProxy(u)
	director := reverse.Director
	reverse.Director = func(r *http.Request) {
		host := r.Host
		director(r)
		r.Host = host
		r.Header.Set("X-Forwarded-Proto", "https")
	}
	reverse.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "regional process unavailable", 503)
	}
	server := httptest.NewTLSServer(reverse)
	t.Cleanup(server.Close)
	return server
}
func stopRegionalProcess(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("process did not stop")
	}
}
func (r *regionalProcess) start() {
	r.harness.run(os.Environ(), "../pool-controller", "./cmd/livepeer-pool-controller", "serve", "--config", r.config, "--data-dir", r.data)
	r.cmd = r.harness.procs[len(r.harness.procs)-1]
	r.harness.waitHealthy(r.memberURL+"/member/v1/terms", "regional controller")
}
func provisionRegionalProcess(t *testing.T, name, trust string) *regionalProcess {
	t.Helper()
	p := &pool{t: t, dir: t.TempDir()}
	t.Cleanup(p.stop)
	r := &regionalProcess{harness: p, data: filepath.Join(p.dir, "state"), config: filepath.Join(p.dir, "controller.yaml")}
	fixture, output := filepath.Join(p.dir, "fixture.json"), filepath.Join(p.dir, "identity.json")
	raw, _ := json.Marshal(map[string]string{"DataDir": r.data, "Version": name + "-v1", "Output": output})
	write(t, fixture, string(raw))
	cmd := exec.Command("go", "test", "./internal/repo", "-run", "^TestRegionalE2EProvisionTerms$", "-count=1")
	cmd.Dir = "../pool-controller"
	cmd.Env = append(os.Environ(), "REGIONAL_E2E_PROVISION="+fixture)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("provision: %v %s", err, out)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var identity struct {
		PoolID string `json:"pool_id"`
	}
	decode(t, raw, &identity)
	r.id = identity.PoolID
	admin, member, metrics := freePort(t), freePort(t), freePort(t)
	r.memberURL = fmt.Sprintf("http://127.0.0.1:%d", member)
	r.proxy = tlsProcessProxy(t, r.memberURL)
	write(t, r.config, fmt.Sprintf(`identity:
  orch_eth_address: %q
  label: %q
listen:
  paid: "127.0.0.1:%d"
  member: "127.0.0.1:%d"
  metrics: "127.0.0.1:%d"
member_issuer_trust_file: %q
`, orchAddress, name, admin, member, metrics, trust))
	r.start()
	return r
}

type portalBrowser struct {
	t      *testing.T
	client *http.Client
	origin string
	cookie *http.Cookie
}

func (b *portalBrowser) call(method, path, body string) (int, []byte) {
	b.t.Helper()
	req, err := http.NewRequest(method, b.origin+path, strings.NewReader(body))
	if err != nil {
		b.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", b.origin)
	if b.cookie != nil {
		req.AddCookie(b.cookie)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	if method == "POST" && path == "/api/auth/login" && resp.StatusCode == 200 {
		for _, c := range resp.Cookies() {
			b.cookie = c
		}
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatal(err)
	}
	return resp.StatusCode, raw
}
func (b *portalBrowser) require(method, path, body string, status int) []byte {
	b.t.Helper()
	got, raw := b.call(method, path, body)
	if got != status {
		b.t.Fatalf("%s %s: %d want %d: %s", method, path, got, status, raw)
	}
	return raw
}
func (b *portalBrowser) signIn() string {
	b.t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		b.t.Fatal(err)
	}
	wallet := crypto.PubkeyToAddress(key.PublicKey).Hex()
	raw := b.require("POST", "/api/auth/challenge", `{"wallet":"`+wallet+`"}`, 200)
	var challenge struct{ ID, Message string }
	decode(b.t, raw, &challenge)
	sig, err := crypto.Sign(accounts.TextHash([]byte(challenge.Message)), key)
	if err != nil {
		b.t.Fatal(err)
	}
	b.require("POST", "/api/auth/login", `{"id":"`+challenge.ID+`","signature":"`+hex.EncodeToString(sig)+`"}`, 200)
	if b.cookie == nil || !b.cookie.Secure || !b.cookie.HttpOnly || b.cookie.SameSite != http.SameSiteStrictMode {
		b.t.Fatal("missing hardened session")
	}
	return strings.ToLower(wallet)
}

func TestRegionalPortalRealControllersRestartAndOutage(t *testing.T) {
	if testing.Short() {
		t.Skip("boots real regional controllers and shared portal")
	}
	p := &pool{t: t, dir: t.TempDir()}
	t.Cleanup(p.stop)
	portalAddr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	proxy := tlsProcessProxy(t, "http://"+portalAddr)
	public, private, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, trust := filepath.Join(p.dir, "issuer.json"), filepath.Join(p.dir, "trust.json")
	now := time.Now()
	raw, _ := json.Marshal(memberauth.PrivateKeyFile{Issuer: proxy.URL, KeyID: "e2e-key", PrivateKey: base64.StdEncoding.EncodeToString(private)})
	write(t, signer, string(raw))
	raw, _ = json.Marshal(memberauth.Trust{Issuer: proxy.URL, Keys: []memberauth.PublicKey{{ID: "e2e-key", PublicKey: base64.StdEncoding.EncodeToString(public), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}}})
	write(t, trust, string(raw))
	eu, us := provisionRegionalProcess(t, "eu", trust), provisionRegionalProcess(t, "us", trust)
	if eu.id == us.id {
		t.Fatal("regional identity collision")
	}
	var regions strings.Builder
	for _, r := range []*regionalProcess{eu, us} {
		ca := filepath.Join(r.harness.dir, "ca.pem")
		write(t, ca, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: r.proxy.Certificate().Raw})))
		fmt.Fprintf(&regions, "  - pool_id: %q\n    name: %q\n    url: %q\n    ca_file: %q\n", r.id, r.id, r.proxy.URL, ca)
	}
	cfg := filepath.Join(p.dir, "portal.yaml")
	write(t, cfg, fmt.Sprintf("listen: %q\npublic_origin: %q\nstate_path: %q\nsigner_file: %q\nregions:\n%s", portalAddr, proxy.URL, filepath.Join(p.dir, "portal.db"), signer, regions.String()))
	startPortal := func() *exec.Cmd {
		p.run(os.Environ(), "../member-portal", "./cmd/livepeer-member-portal", "--config", cfg)
		p.waitHealthy("http://"+portalAddr+"/api/session", "portal")
		return p.procs[len(p.procs)-1]
	}
	portalCmd := startPortal()
	browser := &portalBrowser{t: t, client: proxy.Client(), origin: proxy.URL}
	wallet := browser.signIn()
	memberPath := func(r *regionalProcess) string { return "/api/regions/" + r.id + "/membership" }
	for _, r := range []*regionalProcess{eu, us} {
		raw := browser.require("GET", memberPath(r), "", 200)
		if !strings.Contains(string(raw), `"joined":false`) {
			t.Fatalf("signin joined a region: %s", raw)
		}
	}
	browser.require("POST", "/api/regions/"+eu.id+"/join", `{"pool_id":"`+us.id+`","terms_version":"eu-v1"}`, 409)
	join := func(r *regionalProcess, version string) {
		browser.require("POST", "/api/regions/"+r.id+"/join", `{"pool_id":"`+r.id+`","terms_version":"`+version+`"}`, 200)
	}
	join(eu, "eu-v1")
	if raw := browser.require("GET", memberPath(us), "", 200); !strings.Contains(string(raw), `"joined":false`) {
		t.Fatal("EU join joined US")
	}
	join(us, "us-v1")
	for _, r := range []*regionalProcess{eu, us} {
		raw := browser.require("GET", memberPath(r), "", 200)
		if !strings.Contains(string(raw), wallet) || !strings.Contains(string(raw), `"joined":true`) {
			t.Fatal(string(raw))
		}
	}
	// The token's signature is valid but its destination is EU. US verifies locally.
	token, err := (&memberauth.Signer{Issuer: proxy.URL, KeyID: "e2e-key", PrivateKey: private}).Sign(wallet, eu.id, strings.Repeat("a", 64), time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", us.proxy.URL+"/member/v1/membership", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := us.proxy.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("wrong-region token accepted", resp.StatusCode)
	}
	for _, r := range []*regionalProcess{eu, us} {
		resp, err := r.proxy.Client().Get(r.proxy.URL + "/admin/v1/regional-terms")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatal("member listener exposes admin", resp.StatusCode)
		}
	}
	reports := func() map[string]string {
		raw := browser.require("GET", "/api/reports", "", 200)
		var result struct {
			Wallet  string
			Regions []struct {
				PoolID string `json:"pool_id"`
				Status string
			}
		}
		decode(t, raw, &result)
		if result.Wallet != wallet || len(result.Regions) != 2 {
			t.Fatal(string(raw))
		}
		out := map[string]string{}
		for _, r := range result.Regions {
			out[r.PoolID] = r.Status
		}
		return out
	}
	if statuses := reports(); statuses[eu.id] != "fresh" || statuses[us.id] != "fresh" {
		t.Fatal(statuses)
	}
	stopRegionalProcess(t, eu.cmd)
	if statuses := reports(); statuses[eu.id] != "stale" || statuses[us.id] != "fresh" {
		t.Fatal(statuses)
	}
	browser.require("GET", memberPath(us), "", 200)
	browser.require("POST", "/api/regions/"+eu.id+"/join", `{"pool_id":"`+eu.id+`","terms_version":"eu-v1"}`, 503)
	// Restart the portal during the outage: its in-memory report cache is gone,
	// while its durable wallet session survives. Missing must not become zero.
	stopRegionalProcess(t, portalCmd)
	portalCmd = startPortal()
	_ = portalCmd
	browser.require("GET", "/api/session", "", 200)
	if statuses := reports(); statuses[eu.id] != "unavailable" || statuses[us.id] != "fresh" {
		t.Fatal(statuses)
	}
	eu.start()
	if raw := browser.require("GET", memberPath(eu), "", 200); !strings.Contains(string(raw), `"joined":true`) {
		t.Fatal("membership lost on controller restart", string(raw))
	}
	if statuses := reports(); statuses[eu.id] != "fresh" || statuses[us.id] != "fresh" {
		t.Fatal(statuses)
	}
	other := &portalBrowser{t: t, client: proxy.Client(), origin: proxy.URL}
	otherWallet := other.signIn()
	raw = other.require("GET", memberPath(eu), "", 200)
	if strings.Contains(string(raw), wallet) || !strings.Contains(string(raw), otherWallet) || !strings.Contains(string(raw), `"joined":false`) {
		t.Fatal("cross-wallet membership", string(raw))
	}
	browser.require("POST", "/api/auth/logout", "", 204)
	browser.require("GET", "/api/reports", "", 401)
}
