package utils

import "testing"

func TestIsHTTPSURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://orch.example.com:8935", true},
		{"https://orch.example.com/.well-known/livepeer-ai-registry.json", true},
		{"http://localhost:8935", true},
		{"http://127.0.0.1:8935", true},
		{"http://orch.example.com", false},
		{"ftp://x", false},
		{"", false},
		{"notaurl", false},
	}
	for _, c := range cases {
		if got := IsHTTPSURL(c.in); got != c.want {
			t.Fatalf("IsHTTPSURL(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
