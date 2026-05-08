package types

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func validManifestJSON() string {
	return `{
  "schema_version": "3.0.1",
  "eth_address": "0xabcdef0000000000000000000000000000000000",
  "issued_at": "2026-04-25T15:00:00Z",
  "nodes": [
    {
      "id": "n1",
      "url": "https://orch.example.com:8935",
      "capabilities": [
        {
          "name": "openai:/v1/chat/completions",
          "offerings": [
            {"id": "gpt-oss-20b", "price_per_work_unit_wei": "1000"}
          ]
        }
      ]
    }
  ],
  "signature": {
    "alg": "eth-personal-sign",
    "value": "0x` + strings.Repeat("ab", 65) + `",
    "signed_canonical_bytes_sha256": "0x` + strings.Repeat("cd", 32) + `"
  }
}`
}

func TestDecodeManifest_HappyPath(t *testing.T) {
	m, err := DecodeManifest([]byte(validManifestJSON()))
	if err != nil {
		t.Fatalf("DecodeManifest: %v", err)
	}
	if m.SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion = %s", m.SchemaVersion)
	}
	if m.EthAddress != "0xabcdef0000000000000000000000000000000000" {
		t.Fatalf("EthAddress = %s", m.EthAddress)
	}
	if len(m.Nodes) != 1 || m.Nodes[0].ID != "n1" {
		t.Fatalf("nodes mismatch: %+v", m.Nodes)
	}
}

func TestDecodeManifest_RejectsCases(t *testing.T) {
	mod := func(field, replace string) string {
		return strings.Replace(validManifestJSON(), field, replace, 1)
	}
	cases := []struct {
		name      string
		body      string
		wantSent  error
		wantField string
	}{
		{
			name:     "empty",
			body:     "",
			wantSent: ErrParse,
		},
		{
			name:     "trailing-data",
			body:     validManifestJSON() + " garbage",
			wantSent: ErrParse,
		},
		{
			name:     "unknown-top-field",
			body:     strings.Replace(validManifestJSON(), `"schema_version"`, `"unknown_extra": 1, "schema_version"`, 1),
			wantSent: ErrUnknownField,
		},
		{
			name:      "schema-too-new",
			body:      mod(`"schema_version": "3.0.1"`, `"schema_version": "4.0.0"`),
			wantSent:  ErrInvalidSchemaVersion,
			wantField: "schema_version",
		},
		{
			name:      "mixed-case-eth",
			body:      mod(`"0xabcdef0000000000000000000000000000000000"`, `"0xABCdef0000000000000000000000000000000000"`),
			wantSent:  ErrInvalidEthAddress,
			wantField: "eth_address",
		},
		{
			name:      "no-nodes",
			body:      mod(`"nodes": [`, `"nodes": [], "_dropped": [`),
			wantSent:  ErrUnknownField,
			wantField: "",
		},
		{
			name:      "bad-url-scheme",
			body:      mod(`"https://orch.example.com:8935"`, `"ftp://x"`),
			wantSent:  ErrInvalidNodeURL,
			wantField: "nodes[0].url",
		},
		{
			name:      "non-decimal-price",
			body:      mod(`"price_per_work_unit_wei": "1000"`, `"price_per_work_unit_wei": "1.5"`),
			wantSent:  ErrParse,
			wantField: "nodes[0].capabilities[0].offerings[0].price_per_work_unit_wei",
		},
		{
			name:      "bad-sig-alg",
			body:      mod(`"alg": "eth-personal-sign"`, `"alg": "future-typed-sign"`),
			wantSent:  ErrSignatureMalformed,
			wantField: "signature.alg",
		},
		{
			name:      "bad-sig-len",
			body:      mod(`"value": "0x`+strings.Repeat("ab", 65)+`"`, `"value": "0xdeadbeef"`),
			wantSent:  ErrSignatureMalformed,
			wantField: "signature.value",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := DecodeManifest([]byte(c.body))
			if err == nil {
				t.Fatal("expected error")
			}
			if c.wantSent != nil && !errors.Is(err, c.wantSent) {
				t.Fatalf("want sentinel %v, got %v", c.wantSent, err)
			}
			if c.wantField != "" {
				ve := &ManifestValidationError{}
				if errors.As(err, &ve) && ve.Field != c.wantField {
					t.Fatalf("want field %q, got %q (full: %s)", c.wantField, ve.Field, err)
				}
			}
		})
	}
}

func TestEncodeDecode_RoundTrip(t *testing.T) {
	m, err := DecodeManifest([]byte(validManifestJSON()))
	if err != nil {
		t.Fatal(err)
	}
	enc := MustEncodeManifest(m)
	m2, err := DecodeManifest(enc)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if m2.EthAddress != m.EthAddress {
		t.Fatalf("round-trip mismatch")
	}
}

func TestDecodeManifest_DuplicateNodeIDs(t *testing.T) {
	body := strings.Replace(validManifestJSON(),
		`"capabilities": [
        {
          "name": "openai:/v1/chat/completions",
          "offerings": [
            {"id": "gpt-oss-20b", "price_per_work_unit_wei": "1000"}
          ]
        }
      ]
    }`,
		`"capabilities": []
    },
    {
      "id": "n1",
      "url": "https://orch.example.com:8936",
      "capabilities": []
    }`, 1)
	_, err := DecodeManifest([]byte(body))
	if err == nil {
		t.Fatal("expected duplicate-ID error")
	}
	if !strings.Contains(err.Error(), "duplicate id n1") {
		t.Fatalf("expected duplicate-id message, got %v", err)
	}
}

func TestEncodeManifestPretty_HumanReadable(t *testing.T) {
	m, _ := DecodeManifest([]byte(validManifestJSON()))
	pretty, err := EncodeManifestPretty(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pretty), "\n") {
		t.Fatalf("expected newline-formatted output, got: %s", pretty)
	}
}

// Compile-check that NewValidation produces something that prints with all forms.
func ExampleNewValidation() {
	e := NewValidation(ErrParse, "x.y", "must be present")
	fmt.Println(e.Error())
	// Output: parse_error at x.y: must be present
}
