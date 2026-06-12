package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validPolicyJSON() string {
	return `{
  "policy_version": 1,
  "auto_sign": {"renewal": true, "benign": false},
  "benign_bounds": {
    "price_delta_max_pct": 10,
    "allow_tuple_removal": true,
    "worker_url_domain_allowlist": ["workers.example-orch.net"]
  },
  "rate_limit": {"max_auto_signs_per_hour": 4, "on_breach": "pause"},
  "stability_window_seconds": 300,
  "renewal_threshold_fraction": 0.3333
}`
}

func TestParse_ValidPolicy(t *testing.T) {
	p, err := Parse([]byte(validPolicyJSON()))
	if err != nil {
		t.Fatal(err)
	}
	if !p.AutoSign.Renewal || p.AutoSign.Benign {
		t.Fatalf("auto_sign dials wrong: %+v", p.AutoSign)
	}
	if p.RateLimit.MaxAutoSignsPerHour != 4 {
		t.Fatalf("rate limit: %+v", p.RateLimit)
	}
}

func TestParse_ShippedExampleIsValid(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "examples", "sign-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(body); err != nil {
		t.Fatalf("shipped example must validate: %v", err)
	}
}

func TestParse_FailsClosed(t *testing.T) {
	mutate := func(from, to string) string {
		s := strings.Replace(validPolicyJSON(), from, to, 1)
		if s == validPolicyJSON() {
			t.Fatalf("mutation %q not applied", from)
		}
		return s
	}
	cases := []struct {
		name string
		body string
	}{
		{"unknown field", mutate(`"policy_version": 1,`, `"policy_version": 1, "tpyo_knob": true,`)},
		{"wrong version", mutate(`"policy_version": 1`, `"policy_version": 2`)},
		{"pct over 100", mutate(`"price_delta_max_pct": 10`, `"price_delta_max_pct": 101`)},
		{"negative pct", mutate(`"price_delta_max_pct": 10`, `"price_delta_max_pct": -1`)},
		{"zero rate limit", mutate(`"max_auto_signs_per_hour": 4`, `"max_auto_signs_per_hour": 0`)},
		{"unknown breach behavior", mutate(`"on_breach": "pause"`, `"on_breach": "throttle"`)},
		{"fraction zero", mutate(`"renewal_threshold_fraction": 0.3333`, `"renewal_threshold_fraction": 0`)},
		{"fraction one", mutate(`"renewal_threshold_fraction": 0.3333`, `"renewal_threshold_fraction": 1`)},
		{"negative stability window", mutate(`"stability_window_seconds": 300`, `"stability_window_seconds": -1`)},
		{"allowlist wildcard", mutate(`"workers.example-orch.net"`, `"*.example-orch.net"`)},
		{"allowlist scheme", mutate(`"workers.example-orch.net"`, `"https://workers.example-orch.net"`)},
		{"allowlist uppercase", mutate(`"workers.example-orch.net"`, `"Workers.example-orch.net"`)},
		{"allowlist leading dot", mutate(`"workers.example-orch.net"`, `".example-orch.net"`)},
		{"allowlist empty entry", mutate(`"workers.example-orch.net"`, `""`)},
		{"trailing content", validPolicyJSON() + `{"second": "document"}`},
		{"not json", "renewal: true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.body)); err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

func TestLoad_RecordsFileHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sign-policy.json")
	if err := os.WriteFile(path, []byte(validPolicyJSON()), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.SHA256) != 64 {
		t.Fatalf("sha256=%q want 64 hex chars", loaded.SHA256)
	}

	// Same bytes, same hash; different bytes, different hash.
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.SHA256 != loaded.SHA256 {
		t.Fatal("hash not deterministic")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(validPolicyJSON(), "300", "240", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed.SHA256 == loaded.SHA256 {
		t.Fatal("hash must change with file bytes")
	}
}

func TestLoad_MissingFileFails(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("expected error for missing policy file")
	}
}
