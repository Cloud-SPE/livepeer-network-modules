package portal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberauth"
)

func TestOfflineConfigurationDoesNotCreateStateOrCallRegion(t *testing.T) {
	dir := t.TempDir()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	signerPath := filepath.Join(dir, "issuer.json")
	raw, _ := json.Marshal(memberauth.PrivateKeyFile{Issuer: "https://portal.example", KeyID: "one", PrivateKey: base64.StdEncoding.EncodeToString(key)})
	os.WriteFile(signerPath, raw, 0600)
	path := filepath.Join(dir, "portal.yaml")
	state := filepath.Join(dir, "not-created", "portal.db")
	cfg := map[string]any{"listen": ":8090", "public_origin": "https://portal.example", "state_path": state, "signer_file": signerPath, "regions": []map[string]string{{"pool_id": "eu", "name": "EU", "url": "https://never-contact.invalid"}}}
	write := func() {
		raw, _ := json.Marshal(cfg)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if _, err := LoadConfig(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(state)); !os.IsNotExist(err) {
		t.Fatal("offline validation created state")
	}
	cfg["public_origin"] = "http://portal.example"
	write()
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("plaintext portal accepted")
	}
	cfg["public_origin"] = "https://portal.example"
	cfg["unknown_safety_setting"] = true
	write()
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("unknown config accepted")
	}
}
