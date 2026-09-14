package types

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestPublicationTimeBoundaries(t *testing.T) {
	now := time.Now()
	for _, m := range []*Manifest{nil, {}, {IssuedAt: now, ExpiresAt: now}, {IssuedAt: now.Add(time.Second), ExpiresAt: now.Add(time.Hour)}, {IssuedAt: now.Add(-time.Hour), ExpiresAt: now}} {
		if err := m.ValidAt(now); !errors.Is(err, ErrManifestExpired) {
			t.Fatalf("accepted %+v", m)
		}
	}
	if err := (&Manifest{IssuedAt: now, ExpiresAt: now.Add(time.Second)}).ValidAt(now); err != nil {
		t.Fatal(err)
	}
}
func TestCoordinatorCanonicalPayloadIncludesWindow(t *testing.T) {
	env, err := DecodeCoordinatorEnvelope(envelopeWithExtra(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	a, err := CoordinatorCanonicalBytes(env.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	env.Manifest.ExpiresAt = env.Manifest.ExpiresAt.Add(time.Second)
	b, err := CoordinatorCanonicalBytes(env.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("expiry not signed")
	}
	env.Manifest.Capabilities[0].Extra = map[string]any{"invalid": json.RawMessage("invalid")}
	if _, err := CoordinatorCanonicalBytes(env.Manifest); err == nil {
		t.Fatal("bad raw JSON accepted")
	}
}
func TestCoordinatorRejectsMalformedRequiredFields(t *testing.T) {
	for _, kind := range []string{"spec", "address", "issued", "expiry", "capability", "offering", "protocol", "work", "price", "url", "signature"} {
		t.Run(kind, func(t *testing.T) {
			env, err := DecodeCoordinatorEnvelope(envelopeWithExtra(t, nil))
			if err != nil {
				t.Fatal(err)
			}
			c := &env.Manifest.Capabilities[0]
			switch kind {
			case "spec":
				env.Manifest.SpecVersion = ""
			case "address":
				env.Manifest.Orch.EthAddress = "bad"
			case "issued":
				env.Manifest.IssuedAt = time.Time{}
			case "expiry":
				env.Manifest.ExpiresAt = time.Time{}
			case "capability":
				c.CapabilityID = ""
			case "offering":
				c.OfferingID = ""
			case "protocol":
				c.Protocol = ""
			case "work":
				c.WorkUnit.Name = ""
			case "price":
				c.PricePerUnitWei = "-1"
			case "url":
				c.WorkerURL = "http://remote.example.com"
			case "signature":
				env.Signature.Algorithm = "unknown"
			}
			raw, err := json.Marshal(env)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeCoordinatorEnvelope(raw); err == nil {
				t.Fatal("malformed publication accepted")
			}
		})
	}
}
