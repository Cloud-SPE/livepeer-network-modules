package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func baseOfferConfig(offers ...Offer) *Config {
	return &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Offers:   offers,
	}
}

func jobOffer(id string) Offer {
	return Offer{
		OfferingID: id,
		Capability: "openai:chat-completions",
		Protocol:   "paid-job/v1",
		Match:      map[string]string{"identity.openai.model": "llama-3-70b"},
		Price:      Price{AmountWei: "210000000", PerUnits: 1},
		Capacity:   OfferCapacity{MaxInFlight: 4, QueueLimit: 8},
		Extra:      map[string]any{"region": "us-west-2"},
		Certification: []CertificationStep{
			{Name: "ready", Type: "readiness"},
			{Name: "smoke", Type: "request", Config: map[string]any{
				"body":   map[string]any{"model": "{{identity.openai.model}}"},
				"assert": []any{"$.choices[0].message.content"}}},
			{Name: "usage", Type: "usage"},
			{Name: "latency", Type: "latency", Required: boolPtr(false), Config: map[string]any{"samples": 3, "p50_max_ms": 4000}},
		},
	}
}

func boolPtr(b bool) *bool { return &b }

func TestOffersMinimalJobValidates(t *testing.T) {
	cfg := baseOfferConfig(jobOffer("llama-3-70b-shared"))
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	if cfg.OffersSource != OffersSourceFile {
		t.Fatalf("offers_source default = %q", cfg.OffersSource)
	}
	if !cfg.Offers[0].Certification[0].IsRequired() || cfg.Offers[0].Certification[3].IsRequired() {
		t.Fatal("required default wrong")
	}
}

func TestOffersRejections(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"duplicate id", func(c *Config) { c.Offers = append(c.Offers, jobOffer("llama-3-70b-shared")) }, "offering_id repeats"},
		{"bad protocol", func(c *Config) { c.Offers[0].Protocol = "http-reqresp@v0" }, "protocol must be"},
		{"bad match key", func(c *Config) { c.Offers[0].Match = map[string]string{"openai.model": "x"} }, "identity.<dotted key>"},
		{"empty match value", func(c *Config) { c.Offers[0].Match = map[string]string{"identity.openai.model": ""} }, "must not be empty"},
		{"bad price", func(c *Config) { c.Offers[0].Price.AmountWei = "1.5" }, "price.amount_wei"},
		{"zero per_units", func(c *Config) { c.Offers[0].Price.PerUnits = 0 }, "per_units"},
		{"x- in extra", func(c *Config) { c.Offers[0].Extra["x-quant"] = "fp8" }, "promote them with extra_from_runner"},
		{"reserved extra", func(c *Config) { c.Offers[0].Extra["job"] = "x" }, "reserved"},
		{"promote non-x", func(c *Config) { c.Offers[0].ExtraFromRunner = []string{"quant"} }, "must be an x-* key"},
		{"promote suggested", func(c *Config) { c.Offers[0].ExtraFromRunner = []string{"x-certification-suggested"} }, "never promoted"},
		{"session_policy on job", func(c *Config) { c.Offers[0].SessionPolicy = &SessionPolicy{} }, "only valid for paid-session"},
		{"session without store", func(c *Config) { c.Offers[0].Protocol = "paid-session/v1" }, "require session_store"},
		{"negative capacity", func(c *Config) { c.Offers[0].Capacity.MaxInFlight = -1 }, "capacity"},
		{"admin source with offers", func(c *Config) {
			c.OffersSource = OffersSourceAdmin
			c.AdminAuth = AuthConfig{Method: "bearer", SecretRef: "env://T"}
		}, "offers[] must be empty"},
		{"admin source without admin auth", func(c *Config) { c.OffersSource = OffersSourceAdmin; c.Offers = nil }, "requires admin_auth"},
		{"nothing declared", func(c *Config) { c.Offers = nil }, "must declare at least one"},
		// certification
		{"step unknown type", func(c *Config) { c.Offers[0].Certification[0].Type = "benchmark" }, "type must be"},
		{"step dup name", func(c *Config) { c.Offers[0].Certification[1].Name = "ready" }, "name repeats"},
		{"step unknown key", func(c *Config) { c.Offers[0].Certification[1].Config["expect_stauts"] = 200 }, "not a request key"},
		{"livepeer header", func(c *Config) {
			c.Offers[0].Certification[1].Config["headers"] = map[string]any{"Livepeer-Payment": "x"}
		}, "Livepeer-* headers are forbidden"},
		{"parts without multipart", func(c *Config) {
			c.Offers[0].Certification[1].Config = map[string]any{"parts": []any{map[string]any{"name": "f", "value": "v"}}}
		}, "requires transport: multipart"},
		{"usage before request", func(c *Config) {
			c.Offers[0].Certification = []CertificationStep{{Name: "u", Type: "usage"}}
		}, "no preceding request"},
		{"latency without bound", func(c *Config) { delete(c.Offers[0].Certification[3].Config, "p50_max_ms") }, "p50_max_ms / p95_max_ms"},
		{"fixture both forms", func(c *Config) {
			c.Offers[0].Certification[1].Config = map[string]any{"transport": "multipart", "parts": []any{map[string]any{
				"name": "file", "fixture": map[string]any{"ref": "a/b", "inline_base64": "AAAA", "content_type": "audio/wav"}}}}
		}, "exactly one of ref / inline_base64"},
		{"bad path", func(c *Config) { c.Offers[0].Certification[1].Config["path"] = "/x/../y" }, "path must be relative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseOfferConfig(jobOffer("llama-3-70b-shared"))
			tc.mut(cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v; want containing %q", err, tc.want)
			}
		})
	}
}

func TestOffersSessionValidates(t *testing.T) {
	cfg := baseOfferConfig(Offer{
		OfferingID: "meet-default",
		Capability: "livepeer:meet/sfu-room",
		Protocol:   "paid-session/v1",
		Price:      Price{AmountWei: "10", PerUnits: 1},
		SessionPolicy: &SessionPolicy{Refill: "bounded", LeasePolicy: "fixed", LeaseMaxSeconds: 600,
			Heartbeat: SessionHeartbeat{IntervalSeconds: 10, MissedThreshold: 3}},
		Certification: []CertificationStep{
			{Name: "open", Type: "request", Config: map[string]any{
				"session_params":           map[string]any{"room_name": "cert-{{run.id}}"},
				"expect_descriptor_schema": "sfu-room/v1", "hold_ms": 5000}},
			{Name: "usage", Type: "usage", Config: map[string]any{"window_ms": 5000}},
		},
	})
	cfg.SessionStore = SessionStore{Path: "/tmp/x.db", SealingKeyFile: "/tmp/k"}
	cfg.ExternalBaseURL = "https://broker.example"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	cfg.Offers[0].Certification[0].Config["body"] = map[string]any{}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "session_params, not transport/body/parts") {
		t.Fatalf("session request with body: %v", err)
	}
	cfg.Offers[0].Certification[0].Config = map[string]any{"session_params": map[string]any{}}
	cfg.Offers[0].SessionPolicy.LeasePolicy = "fixed"
	cfg.Offers[0].SessionPolicy.LeaseMaxSeconds = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "lease_max_seconds") {
		t.Fatalf("fixed lease without max: %v", err)
	}
}

func TestOffersCoexistWithLegacyButNotSameOffering(t *testing.T) {
	cfg := baseOfferConfig(jobOffer("shared"))
	cfg.Capabilities = []Capability{{
		ID: "openai:chat-completions", OfferingID: "legacy", Protocol: "paid-job/v1",
		Job:      &JobCapability{Transports: []string{"unary"}},
		WorkUnit: WorkUnit{Name: "tokens", Extractor: map[string]any{"type": "openai-usage"}},
		Price:    Price{AmountWei: "1", PerUnits: 1},
		Backend:  Backend{Transport: "http", URL: "http://b:8000/v1/chat/completions"},
		Extra:    map[string]any{"openai": map[string]any{"model": "m"}, "provider": "vllm"},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("coexist: %v", err)
	}
	cfg.Capabilities[0].OfferingID = "shared"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "also declared under capabilities[]") {
		t.Fatalf("same offering in both: %v", err)
	}
}

// The shipped example must load: it is the operator's starting point.
func TestExampleOffersConfigLoads(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "host-config.offers.example.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skip("example not present")
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%s) = %v", path, err)
	}
	if len(cfg.Offers) == 0 {
		t.Fatal("example declares no offers")
	}
}
