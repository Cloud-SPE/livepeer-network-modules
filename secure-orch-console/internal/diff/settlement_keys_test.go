package diff

import "testing"

const keyA = "0x049d2193d32d9379271df49fcdd6d2b53dad719371ddfb77009494d2c08ceca2bbea717657d9e62d49f11ac13f8ee3ae9dbeea45c1db363ed200edd9618f027f48"

const withKeys = `{
  "spec_version": "0.2.0",
  "publication_seq": 6,
  "issued_at": "2026-05-01T00:00:00Z",
  "expires_at": "2026-06-01T00:00:00Z",
  "orch": {"eth_address": "0xaaaa00000000000000000000000000000000aaaa"},
  "settlement_keys": [
    {"public_key": "` + keyA + `", "not_before": "2026-09-07T00:00:00Z", "expires_at": "2027-09-07T00:00:00Z"}
  ],
  "capabilities": [
    {"capability_id": "openai:chat", "offering_id": "small", "price_per_unit_wei": "1000"},
    {"capability_id": "openai:chat", "offering_id": "large", "price_per_unit_wei": "5000"}
  ]
}`

// A delegation added with no tuple change must still register as a
// change: the tuple diff is empty, and the header carries it.
func TestCompute_SettlementKeysAddedIsAHeaderChange(t *testing.T) {
	r, err := Compute([]byte(before), []byte(withKeys))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Added)+len(r.Removed)+len(r.Changed) != 0 {
		t.Fatalf("tuples should be unchanged: %+v", r)
	}
	if r.Header.SettlementKeysStable {
		t.Fatal("settlement_keys went 0 → 1 and the header calls it stable")
	}
	if len(r.Header.BeforeSettlementKeys) != 0 || len(r.Header.AfterSettlementKeys) != 1 {
		t.Fatalf("before=%d after=%d", len(r.Header.BeforeSettlementKeys), len(r.Header.AfterSettlementKeys))
	}
	if got := r.Header.AfterSettlementKeys[0]["public_key"]; got != keyA {
		t.Fatalf("public_key = %v", got)
	}
}

// The header names the brokers the candidate sells through, so the
// review page can tell the operator where to check each key.
func TestCompute_HeaderListsWorkerURLs(t *testing.T) {
	r, err := Compute(nil, []byte(`{"spec_version":"0.2.0","publication_seq":1,"orch":{"eth_address":"0xaaaa00000000000000000000000000000000aaaa"},
	  "capabilities":[
	    {"capability_id":"a","offering_id":"1","worker_url":"https://b.example"},
	    {"capability_id":"b","offering_id":"1","worker_url":"https://a.example"},
	    {"capability_id":"c","offering_id":"1","worker_url":"https://b.example"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Header.WorkerURLs) != 2 || r.Header.WorkerURLs[0] != "https://a.example" || r.Header.WorkerURLs[1] != "https://b.example" {
		t.Fatalf("worker_urls = %v", r.Header.WorkerURLs)
	}
}

func TestCompute_SettlementKeysUnchangedIsStable(t *testing.T) {
	r, err := Compute([]byte(withKeys), []byte(withKeys))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Header.SettlementKeysStable {
		t.Fatal("identical keys reported as changed")
	}
	first, err := Compute(nil, []byte(withKeys))
	if err != nil {
		t.Fatal(err)
	}
	if !first.Header.SettlementKeysStable || len(first.Header.AfterSettlementKeys) != 1 {
		t.Fatalf("first sign: stable=%v after=%d", first.Header.SettlementKeysStable, len(first.Header.AfterSettlementKeys))
	}
}
