package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
)

// The catalog is loaded once at startup and then has to reach three
// places: the admin listener, the member listener, and the offering
// view. Each of those failing looks like something else — no template
// can be enabled or priced, every certification start is rejected as
// "not in the catalog", and the member bundle ships no runner services
// at all — so the wiring itself is worth pinning rather than the
// symptoms.
func TestServeMuxServesTheLoadedCatalog(t *testing.T) {
	dir := t.TempDir()
	catalogDir := filepath.Join(dir, "templates")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	body := "" +
		"id: wiring-check\n" +
		"capability: openai:chat-completions\n" +
		"offering_id: wiring-check\n" +
		"protocol: paid-job/v1\n" +
		"price_default: { amount_wei: \"7\", per_units: 1 }\n" +
		"stacking: { primary: true }\n"
	if err := os.WriteFile(filepath.Join(catalogDir, "wiring-check.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	catalog, err := templates.Load(catalogDir)
	if err != nil {
		t.Fatalf("templates.Load() error = %v", err)
	}
	if catalog.Len() != 1 {
		t.Fatalf("catalog.Len() = %d, want 1", catalog.Len())
	}

	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("identity:\n  orch_eth_address: 0x123\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	stateRepo, err := repo.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()
	state := &runtimeState{
		configPath: configPath,
		repo:       stateRepo,
		catalog:    catalog,
		cfg:        &config.Config{Identity: config.Identity{OrchEthAddress: "0x123"}},
	}
	server := httptest.NewServer(newServeMux(state))
	defer server.Close()

	resp, err := http.Get(server.URL + "/admin/v1/template-catalog")
	if err != nil {
		t.Fatalf("GET /admin/v1/template-catalog error = %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("template-catalog status = %d: %s", resp.StatusCode, raw)
	}
	var payload struct {
		Templates []struct {
			ID             string `json:"id"`
			Enabled        bool   `json:"enabled"`
			EffectivePrice struct {
				AmountWei string `json:"amount_wei"`
			} `json:"effective_price"`
		} `json:"templates"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode template-catalog: %v (%s)", err, raw)
	}
	if len(payload.Templates) != 1 || payload.Templates[0].ID != "wiring-check" {
		t.Fatalf("template-catalog did not serve the loaded catalog: %s", raw)
	}
	// Not enabled until the pool says so, and priced from the catalog
	// until the pool overrides it.
	if payload.Templates[0].Enabled {
		t.Fatalf("template is enabled with no override recorded: %s", raw)
	}
	if got := payload.Templates[0].EffectivePrice.AmountWei; got != "7" {
		t.Fatalf("effective_price.amount_wei = %q, want the catalog default 7", got)
	}
}
