package types

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCanonicalBytes_DeterministicAcrossOrder(t *testing.T) {
	// Two manifests built from the same underlying data but with
	// nondeterministic ordering in opaque blobs (Capability.Extra,
	// Model.Constraints). Their canonical bytes must be identical.
	a := &Manifest{
		SchemaVersion: SchemaVersion,
		EthAddress:    "0xabcdef0000000000000000000000000000000000",
		IssuedAt:      time.Unix(1745000000, 0).UTC(),
		Nodes: []Node{
			{
				ID:  "n1",
				URL: "https://orch.example.com:8935",
				Capabilities: []Capability{
					{
						Name:  "openai:/v1/chat/completions",
						Extra: json.RawMessage(`{"b":2,"a":1}`),
					},
				},
			},
		},
	}
	b := &Manifest{
		SchemaVersion: SchemaVersion,
		EthAddress:    "0xabcdef0000000000000000000000000000000000",
		IssuedAt:      time.Unix(1745000000, 0).UTC(),
		Nodes: []Node{
			{
				ID:  "n1",
				URL: "https://orch.example.com:8935",
				Capabilities: []Capability{
					{
						Name:  "openai:/v1/chat/completions",
						Extra: json.RawMessage(`{"a":1,"b":2}`),
					},
				},
			},
		},
	}

	ca, err := CanonicalBytes(a)
	if err != nil {
		t.Fatalf("CanonicalBytes(a): %v", err)
	}
	cb, err := CanonicalBytes(b)
	if err != nil {
		t.Fatalf("CanonicalBytes(b): %v", err)
	}
	if string(ca) != string(cb) {
		t.Fatalf("canonical bytes differ:\n  a=%s\n  b=%s", ca, cb)
	}
}

func TestCanonicalBytes_ZerosSignature(t *testing.T) {
	m := &Manifest{
		SchemaVersion: SchemaVersion,
		EthAddress:    "0xabcdef0000000000000000000000000000000000",
		IssuedAt:      time.Unix(1745000000, 0).UTC(),
		Nodes:         []Node{{ID: "n1", URL: "https://x", Capabilities: []Capability{}}},
		Signature: Signature{
			Alg:                        "eth-personal-sign",
			Value:                      "0xdeadbeef",
			SignedCanonicalBytesSHA256: "0xdead",
		},
	}
	c, err := CanonicalBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(c), "deadbeef") {
		t.Fatalf("canonical bytes leaked signature: %s", c)
	}
	// And the input is unmutated.
	if m.Signature.Value != "0xdeadbeef" {
		t.Fatalf("canonical mutated input signature: %v", m.Signature)
	}
}

func TestCanonicalBytes_KeysSorted(t *testing.T) {
	m := &Manifest{
		SchemaVersion: SchemaVersion,
		EthAddress:    "0xabcdef0000000000000000000000000000000000",
		IssuedAt:      time.Unix(1745000000, 0).UTC(),
		Nodes: []Node{
			{ID: "n", URL: "https://x", Capabilities: []Capability{
				{Name: "z", Extra: json.RawMessage(`{"z":1,"a":2}`)},
			}},
		},
	}
	c, err := CanonicalBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	// "a":2 must appear before "z":1 in the Extra blob after canonicalization.
	idxA := strings.Index(string(c), `"a":2`)
	idxZ := strings.Index(string(c), `"z":1`)
	if idxA < 0 || idxZ < 0 {
		t.Fatalf("expected both keys present; got %s", c)
	}
	if idxA > idxZ {
		t.Fatalf("expected 'a' before 'z'; got %s", c)
	}
}

func TestCanonicalSHA256_Stable(t *testing.T) {
	c := []byte(`{"hello":"world"}`)
	h1 := CanonicalSHA256(c)
	h2 := CanonicalSHA256(c)
	if h1 != h2 {
		t.Fatalf("hash not stable: %s vs %s", h1, h2)
	}
	if !strings.HasPrefix(h1, "0x") || len(h1) != 66 {
		t.Fatalf("malformed hash: %s", h1)
	}
}
