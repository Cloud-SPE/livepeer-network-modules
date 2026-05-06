package diff

import (
	"testing"
)

const before = `{
  "manifest": {
    "spec_version": "0.2.0",
    "publication_seq": 5,
    "issued_at": "2026-04-01T00:00:00Z",
    "expires_at": "2026-05-01T00:00:00Z",
    "orch": {"eth_address": "0xaaaa00000000000000000000000000000000aaaa"},
    "capabilities": [
      {"capability_id": "openai:chat", "offering_id": "small", "price_per_unit_wei": "1000"},
      {"capability_id": "openai:chat", "offering_id": "large", "price_per_unit_wei": "5000"}
    ]
  },
  "signature": {"value": "0x00"}
}`

const after = `{
  "manifest": {
    "spec_version": "0.2.0",
    "publication_seq": 6,
    "issued_at": "2026-05-01T00:00:00Z",
    "expires_at": "2026-06-01T00:00:00Z",
    "orch": {"eth_address": "0xaaaa00000000000000000000000000000000aaaa"},
    "capabilities": [
      {"capability_id": "openai:chat", "offering_id": "small", "price_per_unit_wei": "1100"},
      {"capability_id": "video:transcode", "offering_id": "h264", "price_per_unit_wei": "200"}
    ]
  },
  "signature": {"value": "0x00"}
}`

func TestCompute_FullDiff(t *testing.T) {
	r, err := Compute([]byte(before), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Added) != 1 || r.Added[0].CapabilityID != "video:transcode" {
		t.Fatalf("unexpected Added: %+v", r.Added)
	}
	if len(r.Removed) != 1 || r.Removed[0].OfferingID != "large" {
		t.Fatalf("unexpected Removed: %+v", r.Removed)
	}
	if len(r.Changed) != 1 || r.Changed[0].OfferingID != "small" {
		t.Fatalf("unexpected Changed: %+v", r.Changed)
	}
	if len(r.Unchanged) != 0 {
		t.Fatalf("unexpected Unchanged: %+v", r.Unchanged)
	}
	if !r.Header.SeqMonotonic {
		t.Fatalf("seq not monotonic: before=%v after=%d", r.Header.BeforeSeq, r.Header.AfterSeq)
	}
	if !r.Header.EthAddressStable {
		t.Fatal("eth address should be stable")
	}
}

func TestCompute_FirstSign(t *testing.T) {
	r, err := Compute(nil, []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Added) != 2 {
		t.Fatalf("expected both tuples Added, got %d", len(r.Added))
	}
	if !r.Header.SeqMonotonic {
		t.Fatal("first sign should be considered monotonic")
	}
}

func TestCompute_RejectsNoCandidate(t *testing.T) {
	if _, err := Compute([]byte(before), nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestCompute_DetectsAddressChange(t *testing.T) {
	swapped := `{
      "manifest": {
        "spec_version": "0.2.0",
        "publication_seq": 6,
        "issued_at": "2026-05-01T00:00:00Z",
        "expires_at": "2026-06-01T00:00:00Z",
        "orch": {"eth_address": "0xbbbb00000000000000000000000000000000bbbb"},
        "capabilities": []
      },
      "signature": {"value": "0x00"}
    }`
	r, err := Compute([]byte(before), []byte(swapped))
	if err != nil {
		t.Fatal(err)
	}
	if r.Header.EthAddressStable {
		t.Fatal("expected eth address change to be flagged")
	}
}

func TestCompute_DetectsRollback(t *testing.T) {
	rolledBack := `{
      "manifest": {
        "spec_version": "0.2.0",
        "publication_seq": 4,
        "issued_at": "2026-05-01T00:00:00Z",
        "expires_at": "2026-06-01T00:00:00Z",
        "orch": {"eth_address": "0xaaaa00000000000000000000000000000000aaaa"},
        "capabilities": []
      },
      "signature": {"value": "0x00"}
    }`
	r, err := Compute([]byte(before), []byte(rolledBack))
	if err != nil {
		t.Fatal(err)
	}
	if r.Header.SeqMonotonic {
		t.Fatal("expected non-monotonic flag")
	}
}
