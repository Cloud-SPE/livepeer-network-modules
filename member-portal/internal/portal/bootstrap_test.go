package portal

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBootstrapPreparedPinnedOneUseAndRestart(t *testing.T) {
	dir := t.TempDir()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	file, _ := archive.Create("docker-compose.yaml")
	file.Write([]byte("services: {}\n"))
	archive.Close()
	bundle := buffer.Bytes()
	region := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/member/v1/enrollments/host-id/bundle" || r.Header.Get("Authorization") != "Bearer enrollment-secret" {
			t.Error("bundle request identity", r.URL.Path)
			http.Error(w, "bad", 403)
			return
		}
		w.Write(bundle)
	}))
	defer region.Close()
	ca := filepath.Join(dir, "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: region.Certificate().Raw}), 0600)
	client, err := NewRegionalClient(Region{PoolID: "eu", Name: "EU", URL: region.URL, CAFile: ca}, "unused")
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(filepath.Join(dir, "portal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	server := &Server{Store: store, Origin: "https://portal.example"}
	wallet := "0x" + strings.Repeat("1", 40)
	raw := []byte(`{"enrollment":{"id":"host-id","pool_id":"eu","member_eth_address":"` + wallet + `"},"token":"enrollment-secret","bundle_url":"https://evil.example/steal"}`)
	result, err := server.prepareEnrollment(context.Background(), client, Session{Wallet: wallet}, raw)
	if err != nil {
		t.Fatal(err)
	}
	data := result.(map[string]any)
	url := data["bundle_url"].(string)
	secret := strings.Split(url, "/")[4]
	if strings.Contains(data["bootstrap_command"].(string), "enrollment-secret") {
		t.Fatal("credential leaked")
	}
	store.Close()
	store, err = OpenStore(filepath.Join(dir, "portal.db"))
	if err != nil {
		t.Fatal(err)
	}
	server.Store = store
	handler, err := server.Handler()
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://portal.example"+path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := call("GET", "/install/"+secret)
	if w.Code != 200 || !strings.Contains(w.Body.String(), ".local/share/livepeer-pool/host-id") || strings.Contains(w.Body.String(), "enrollment-secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	syntax := exec.Command("sh", "-n")
	syntax.Stdin = strings.NewReader(w.Body.String())
	if err := syntax.Run(); err != nil {
		t.Fatal("invalid installer shell", err)
	}
	if w := call("HEAD", "/bootstrap/"+secret+"/bundle"); w.Code != 405 {
		t.Fatal("HEAD consumed bundle", w.Code)
	}
	w = call("GET", "/bootstrap/"+secret+"/bundle")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), bundle) {
		t.Fatal(w.Code)
	}
	if w := call("GET", "/bootstrap/"+secret+"/bundle"); w.Code != 410 {
		t.Fatal("bundle replay", w.Code)
	}
	if w := call("GET", "/install/"+secret); w.Code != 410 {
		t.Fatal("consumed install still available")
	}
	_, err = server.prepareEnrollment(context.Background(), client, Session{Wallet: "other"}, raw)
	if err == nil {
		t.Fatal("cross-wallet bundle")
	}
	expired, err := store.saveBootstrap(Bootstrap{PoolID: "eu", EnrollmentID: "host-id", ExpiresAt: time.Now().Add(-time.Second), Bundle: bundle})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.bootstrap(expired, true); err == nil {
		t.Fatal("expired bootstrap accepted")
	}
}
