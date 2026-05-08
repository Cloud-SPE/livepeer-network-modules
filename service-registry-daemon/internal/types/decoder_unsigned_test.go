package types

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const validUnsignedManifestJSON = `{
  "schema_version": "3.0.1",
  "eth_address": "0xabcdef0000000000000000000000000000000000",
  "issued_at": "2026-04-25T15:00:00Z",
  "nodes": [
    {
      "id": "n1",
      "url": "https://orch.example.com:8935",
      "capabilities": [
        {"name": "openai:/v1/chat/completions"}
      ]
    }
  ],
  "signature": {"alg": "eth-personal-sign", "value": "", "signed_canonical_bytes_sha256": ""}
}`

func TestDecodeUnsignedManifest_HappyPath(t *testing.T) {
	m, err := DecodeUnsignedManifest([]byte(validUnsignedManifestJSON))
	if err != nil {
		t.Fatalf("DecodeUnsignedManifest: %v", err)
	}
	if m.SchemaVersion != SchemaVersion {
		t.Fatalf("schema: %s", m.SchemaVersion)
	}
	if m.Signature.Value != "" {
		t.Fatalf("expected unsigned, got value=%s", m.Signature.Value)
	}
}

func TestDecodeUnsignedManifest_RejectsEmpty(t *testing.T) {
	if _, err := DecodeUnsignedManifest(nil); !errors.Is(err, ErrParse) {
		t.Fatalf("nil body: got %v", err)
	}
	if _, err := DecodeUnsignedManifest([]byte{}); !errors.Is(err, ErrParse) {
		t.Fatalf("empty body: got %v", err)
	}
}

func TestDecodeUnsignedManifest_RejectsBadSchema(t *testing.T) {
	body := strings.Replace(validUnsignedManifestJSON, `"schema_version": "3.0.1"`, `"schema_version": "4.0.0"`, 1)
	if _, err := DecodeUnsignedManifest([]byte(body)); !errors.Is(err, ErrInvalidSchemaVersion) {
		t.Fatalf("got %v", err)
	}
}

func TestDecodeUnsignedManifest_RejectsBadEth(t *testing.T) {
	body := strings.Replace(validUnsignedManifestJSON, "0xabcdef0000000000000000000000000000000000", "0xABCdef0000000000000000000000000000000000", 1)
	if _, err := DecodeUnsignedManifest([]byte(body)); !errors.Is(err, ErrInvalidEthAddress) {
		t.Fatalf("got %v", err)
	}
}

func TestDecodeUnsignedManifest_RejectsTrailingData(t *testing.T) {
	if _, err := DecodeUnsignedManifest([]byte(validUnsignedManifestJSON + " junk")); !errors.Is(err, ErrParse) {
		t.Fatalf("got %v", err)
	}
}

func TestDecodeUnsignedManifest_RejectsExpiresAtField(t *testing.T) {
	body := strings.Replace(validUnsignedManifestJSON,
		`"issued_at": "2026-04-25T15:00:00Z"`,
		`"issued_at": "2026-04-25T15:00:00Z", "expires_at": "2026-04-25T14:00:00Z"`,
		1,
	)
	if _, err := DecodeUnsignedManifest([]byte(body)); !errors.Is(err, ErrUnknownField) {
		t.Fatalf("got %v", err)
	}
}

func TestDecodeUnsignedManifest_RejectsEmptyNodes(t *testing.T) {
	body := strings.Replace(validUnsignedManifestJSON,
		`"nodes": [
    {
      "id": "n1",
      "url": "https://orch.example.com:8935",
      "capabilities": [
        {"name": "openai:/v1/chat/completions"}
      ]
    }
  ]`,
		`"nodes": []`, 1)
	if _, err := DecodeUnsignedManifest([]byte(body)); !errors.Is(err, ErrEmptyNodes) {
		t.Fatalf("got %v", err)
	}
}

func TestIssuedAtRFC3339_RoundTrip(t *testing.T) {
	tt, _ := time.Parse(time.RFC3339, "2026-04-25T15:00:00Z")
	out := IssuedAtRFC3339(tt)
	if out != "2026-04-25T15:00:00Z" {
		t.Fatalf("got %s", out)
	}
}

func TestEthAddress_String(t *testing.T) {
	a, _ := ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	if a.String() != "0xabcdef0000000000000000000000000000000000" {
		t.Fatalf("String() = %s", a.String())
	}
}

func TestMustEncodeManifest_PanicsOnInvalid(t *testing.T) {
	// MustEncodeManifest should not panic on a well-formed Manifest;
	// just sanity-check that it produces non-empty output.
	m, _ := DecodeUnsignedManifest([]byte(validUnsignedManifestJSON))
	out := MustEncodeManifest(m)
	if len(out) == 0 {
		t.Fatal("expected non-empty bytes")
	}
}
