package types

import "testing"

func TestBrokerOfferings_Validate_HappyPath(t *testing.T) {
	b := &BrokerOfferings{
		OrchEthAddress: "0xABCDEF1234567890ABCDEF1234567890ABCDEF12",
		Capabilities: []BrokerOffering{{
			CapabilityID:    "cap",
			OfferingID:      "off",
			InteractionMode: "http-stream@v1",
			WorkUnit:        WorkUnit{Name: "tokens"},
			PricePerUnitWei: "1000",
		}},
	}
	if err := b.Validate("0xabcdef1234567890abcdef1234567890abcdef12"); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerOfferings_Validate_RejectsOrchMismatch(t *testing.T) {
	b := &BrokerOfferings{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Capabilities:   []BrokerOffering{},
	}
	if err := b.Validate("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestBrokerOfferings_Validate_RejectsBadPrice(t *testing.T) {
	b := &BrokerOfferings{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Capabilities: []BrokerOffering{{
			CapabilityID:    "cap",
			OfferingID:      "off",
			InteractionMode: "http-stream@v1",
			WorkUnit:        WorkUnit{Name: "tokens"},
			PricePerUnitWei: "-5",
		}},
	}
	if err := b.Validate("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("expected price error")
	}
}

func TestParseSignedManifest_RejectsUnknownField(t *testing.T) {
	raw := []byte(`{"manifest":{"spec_version":"0.1.0"},"signature":{},"extra":1}`)
	if _, err := ParseSignedManifest(raw); err == nil {
		t.Fatal("expected unknown-field error")
	}
}

func TestParseSignedManifest_RejectsTrailingData(t *testing.T) {
	raw := []byte(`{"manifest":{"spec_version":"0.1.0","publication_seq":0,"issued_at":"2026-05-06T00:00:00Z","expires_at":"2026-05-07T00:00:00Z","orch":{"eth_address":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"capabilities":[]},"signature":{"algorithm":"secp256k1","value":"0xab"}}{}`)
	if _, err := ParseSignedManifest(raw); err == nil {
		t.Fatal("expected trailing-data error")
	}
}
