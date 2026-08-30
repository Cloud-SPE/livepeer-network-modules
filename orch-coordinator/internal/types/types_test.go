package types

import (
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/version"
	"strings"
	"testing"
)

func TestBrokerOfferings_Validate_HappyPath(t *testing.T) {
	b := &BrokerOfferings{
		SpecVersion:    version.VERSION,
		OrchEthAddress: "0xABCDEF1234567890ABCDEF1234567890ABCDEF12",
		Capabilities: []BrokerOffering{{
			CapabilityID:    "cap",
			OfferingID:      "off",
			Protocol:        "paid-job/v1",
			Job:             &JobAxes{"transports": []any{"unary", "stream"}},
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
		SpecVersion:    version.VERSION,
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Capabilities:   []BrokerOffering{},
	}
	if err := b.Validate("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestBrokerOfferings_Validate_RejectsBadPrice(t *testing.T) {
	b := &BrokerOfferings{
		SpecVersion:    version.VERSION,
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Capabilities: []BrokerOffering{{
			CapabilityID:    "cap",
			OfferingID:      "off",
			Protocol:        "paid-job/v1",
			Job:             &JobAxes{"transports": []any{"stream"}},
			WorkUnit:        WorkUnit{Name: "tokens"},
			PricePerUnitWei: "-5",
		}},
	}
	if err := b.Validate("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("expected price error")
	}
}

func TestBrokerOfferings_Validate_RejectsMalformedProtocolTag(t *testing.T) {
	b := &BrokerOfferings{
		SpecVersion:    version.VERSION,
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Capabilities: []BrokerOffering{{
			CapabilityID:    "cap",
			OfferingID:      "off",
			Protocol:        "http-stream@v1",
			WorkUnit:        WorkUnit{Name: "tokens"},
			PricePerUnitWei: "1000",
		}},
	}
	if err := b.Validate("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("expected protocol-tag error for a pre-1.0.0 interaction-mode string")
	}
}

// The manifest schema pairs each protocol with exactly one declared-axes
// object. A tuple that violates the pairing produces a manifest that fails
// schema validation at every resolver, so the scrape boundary rejects it
// rather than letting it reach the cold key.
func TestBrokerOfferings_Validate_ProtocolAxesPairing(t *testing.T) {
	job := &JobAxes{"transports": []any{"unary"}}
	session := &SessionAxes{"descriptor_schema": "rtmp-hls/v1", "metering": "runner-reported"}

	cases := []struct {
		name     string
		protocol string
		job      *JobAxes
		session  *SessionAxes
		wantErr  bool
	}{
		{"job ok", "paid-job/v1", job, nil, false},
		{"job missing axes", "paid-job/v1", nil, nil, true},
		{"job with session axes", "paid-job/v1", job, session, true},
		{"session ok", "paid-session/v1", nil, session, false},
		{"session missing axes", "paid-session/v1", nil, nil, true},
		{"session with job axes", "paid-session/v1", job, session, true},
		{"unknown protocol carries through", "future-thing/v3", nil, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &BrokerOfferings{
				SpecVersion:    version.VERSION,
				OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Capabilities: []BrokerOffering{{
					CapabilityID:    "cap",
					OfferingID:      "off",
					Protocol:        tc.protocol,
					Job:             tc.job,
					Session:         tc.session,
					WorkUnit:        WorkUnit{Name: "tokens"},
					PricePerUnitWei: "1000",
				}},
			}
			err := b.Validate("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			if tc.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseSignedManifest_RejectsUnknownField(t *testing.T) {
	raw := []byte(`{"manifest":{"spec_version":"1.0.0"},"signature":{},"extra":1}`)
	if _, err := ParseSignedManifest(raw); err == nil {
		t.Fatal("expected unknown-field error")
	}
}

func TestParseSignedManifest_RejectsTrailingData(t *testing.T) {
	raw := []byte(`{"manifest":{"spec_version":"1.0.0","publication_seq":0,"issued_at":"2026-05-06T00:00:00Z","expires_at":"2026-05-07T00:00:00Z","orch":{"eth_address":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"capabilities":[]},"signature":{"algorithm":"secp256k1","value":"0xab"}}{}`)
	if _, err := ParseSignedManifest(raw); err == nil {
		t.Fatal("expected trailing-data error")
	}
}

func TestBrokerOfferings_Validate_SpecVersion(t *testing.T) {
	const addr = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cap := BrokerOffering{
		CapabilityID: "cap", OfferingID: "off", Protocol: "paid-job/v1",
		Job:      &JobAxes{"transports": []any{"unary"}},
		WorkUnit: WorkUnit{Name: "tokens"}, PricePerUnitWei: "1",
	}
	cases := []struct {
		name, specVersion, wantErr string
	}{
		{"same version", version.VERSION, ""},
		{"newer minor is fine", bumpMinor(version.VERSION), ""},
		{"older major refused", "1.9.0", "major mismatch"},
		{"newer major refused", "99.0.0", "major mismatch"},
		{"absent refused", "", "required"},
		{"garbage refused", "not-a-version", "major mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &BrokerOfferings{SpecVersion: tc.specVersion, OrchEthAddress: addr, Capabilities: []BrokerOffering{cap}}
			err := b.Validate(addr)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("Validate() = nil, want error containing %q", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.wantErr)
			}
			// Both sides are always named, so an operator knows which to upgrade.
			if err != nil && !strings.Contains(err.Error(), version.VERSION) {
				t.Fatalf("error does not name the coordinator's version: %v", err)
			}
		})
	}
}

// bumpMinor returns the same major with a far-future minor.
func bumpMinor(v string) string {
	major, _, _ := strings.Cut(v, ".")
	return major + ".99.0"
}
