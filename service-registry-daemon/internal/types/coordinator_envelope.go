package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// CoordinatorSignatureAlg is the current orch-coordinator envelope
// signature algorithm marker. The resolver still verifies the
// signature bytes using Ethereum personal-sign recovery; this string
// only identifies the source envelope contract.
const CoordinatorSignatureAlg = "secp256k1"

// CoordinatorSignedManifest is the compatibility envelope currently
// published by orch-coordinator.
type CoordinatorSignedManifest struct {
	Manifest  CoordinatorManifestPayload   `json:"manifest"`
	Signature CoordinatorEnvelopeSignature `json:"signature"`
}

type CoordinatorManifestPayload struct {
	SpecVersion    string                  `json:"spec_version"`
	PublicationSeq uint64                  `json:"publication_seq"`
	IssuedAt       time.Time               `json:"issued_at"`
	ExpiresAt      time.Time               `json:"expires_at"`
	Orch           CoordinatorOrch         `json:"orch"`
	Capabilities   []CoordinatorCapability `json:"capabilities"`
}

type CoordinatorOrch struct {
	EthAddress string `json:"eth_address"`
	ServiceURI string `json:"service_uri,omitempty"`
}

type CoordinatorCapability struct {
	CapabilityID    string              `json:"capability_id"`
	OfferingID      string              `json:"offering_id"`
	InteractionMode string              `json:"interaction_mode"`
	WorkUnit        CoordinatorWorkUnit `json:"work_unit"`
	PricePerUnitWei string              `json:"price_per_unit_wei"`
	WorkerURL       string              `json:"worker_url"`
	Extra           map[string]any      `json:"extra,omitempty"`
	Constraints     map[string]any      `json:"constraints,omitempty"`
}

type CoordinatorWorkUnit struct {
	Name string `json:"name"`
}

type CoordinatorEnvelopeSignature struct {
	Algorithm        string `json:"algorithm"`
	Value            string `json:"value"`
	Canonicalization string `json:"canonicalization,omitempty"`
}

// DecodeCoordinatorEnvelope parses and validates the orch-coordinator
// signed envelope with strict unknown-field rejection.
func DecodeCoordinatorEnvelope(raw []byte) (*CoordinatorSignedManifest, error) {
	if len(raw) == 0 {
		return nil, NewValidation(ErrParse, "", "empty body")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var sm CoordinatorSignedManifest
	if err := dec.Decode(&sm); err != nil {
		return nil, wrapDecodeError(err)
	}
	if dec.More() {
		return nil, NewValidation(ErrParse, "", "trailing data after manifest object")
	}
	if err := validateCoordinatorEnvelope(&sm); err != nil {
		return nil, err
	}
	return &sm, nil
}

func validateCoordinatorEnvelope(sm *CoordinatorSignedManifest) error {
	if sm.Manifest.SpecVersion == "" {
		return NewValidation(ErrParse, "manifest.spec_version", "missing")
	}
	if _, err := ParseEthAddress(sm.Manifest.Orch.EthAddress); err != nil {
		return NewValidation(ErrInvalidEthAddress, "manifest.orch.eth_address", err.Error())
	}
	if hasUpperHex(sm.Manifest.Orch.EthAddress[2:]) {
		return NewValidation(ErrInvalidEthAddress, "manifest.orch.eth_address", "must be lower-cased hex")
	}
	if sm.Manifest.IssuedAt.IsZero() {
		return NewValidation(ErrParse, "manifest.issued_at", "missing")
	}
	if sm.Manifest.ExpiresAt.IsZero() {
		return NewValidation(ErrParse, "manifest.expires_at", "missing")
	}
	for i, c := range sm.Manifest.Capabilities {
		if c.CapabilityID == "" {
			return NewValidation(ErrParse, fmt.Sprintf("manifest.capabilities[%d].capability_id", i), "missing")
		}
		if c.OfferingID == "" {
			return NewValidation(ErrParse, fmt.Sprintf("manifest.capabilities[%d].offering_id", i), "missing")
		}
		if c.InteractionMode == "" {
			return NewValidation(ErrParse, fmt.Sprintf("manifest.capabilities[%d].interaction_mode", i), "missing")
		}
		if c.WorkUnit.Name == "" {
			return NewValidation(ErrParse, fmt.Sprintf("manifest.capabilities[%d].work_unit.name", i), "missing")
		}
		if !isDecimalNonNegInt(c.PricePerUnitWei) {
			return NewValidation(ErrParse, fmt.Sprintf("manifest.capabilities[%d].price_per_unit_wei", i), "must be non-negative decimal integer string")
		}
		if err := validateCoordinatorWorkerURL(c.WorkerURL); err != nil {
			return NewValidation(ErrInvalidNodeURL, fmt.Sprintf("manifest.capabilities[%d].worker_url", i), err.Error())
		}
		if err := validateCoordinatorOpaqueObject(fmt.Sprintf("manifest.capabilities[%d].extra", i), c.Extra); err != nil {
			return err
		}
		if err := validateCoordinatorOpaqueObject(fmt.Sprintf("manifest.capabilities[%d].constraints", i), c.Constraints); err != nil {
			return err
		}
	}
	if sm.Signature.Algorithm != CoordinatorSignatureAlg {
		return NewValidation(ErrSignatureMalformed, "signature.algorithm", fmt.Sprintf("expected %q, got %q", CoordinatorSignatureAlg, sm.Signature.Algorithm))
	}
	if !isHex0x(sm.Signature.Value, 65) {
		return NewValidation(ErrSignatureMalformed, "signature.value", "expected 0x-prefixed 130-hex (65 bytes)")
	}
	return nil
}

func validateCoordinatorWorkerURL(s string) error {
	if s == "" {
		return fmt.Errorf("missing")
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return fmt.Errorf("not a parseable URL")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if strings.HasPrefix(u.Host, "localhost") || strings.HasPrefix(u.Host, "127.0.0.1") {
			return nil
		}
		return fmt.Errorf("http:// only permitted for localhost")
	default:
		return fmt.Errorf("scheme must be https (or http for localhost)")
	}
}

func validateCoordinatorOpaqueObject(path string, v map[string]any) error {
	if v == nil {
		return nil
	}
	if err := validateJSONDepth(v, 1); err != nil {
		return NewValidation(ErrParse, path, err.Error())
	}
	return nil
}

// CoordinatorCanonicalBytes returns the deterministic byte
// representation orch-coordinator signs: the inner manifest payload
// only, re-emitted in lexicographic-key, whitespace-free form.
func CoordinatorCanonicalBytes(m CoordinatorManifestPayload) ([]byte, error) {
	intermediate, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("canonical: marshal: %w", err)
	}
	var anyVal any
	dec := json.NewDecoder(bytes.NewReader(intermediate))
	dec.UseNumber()
	if err := dec.Decode(&anyVal); err != nil {
		return nil, fmt.Errorf("canonical: decode: %w", err)
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, anyVal); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ToManifest projects the coordinator envelope into the resolver's
// node-oriented view. The returned Manifest is synthetic: it exists so
// cache/result plumbing can reuse the existing manifest-based path.
func (sm *CoordinatorSignedManifest) ToManifest() (*Manifest, error) {
	type capKey struct {
		name  string
		extra string
	}
	type capBuilder struct {
		name     string
		workUnit string
		extra    json.RawMessage
		offers   []Offering
	}
	type nodeBuilder struct {
		url   string
		caps  map[capKey]*capBuilder
		order []capKey
	}

	nodesByURL := make(map[string]*nodeBuilder)
	urls := make([]string, 0)
	for _, tuple := range sm.Manifest.Capabilities {
		nb, ok := nodesByURL[tuple.WorkerURL]
		if !ok {
			nb = &nodeBuilder{
				url:  tuple.WorkerURL,
				caps: make(map[capKey]*capBuilder),
			}
			nodesByURL[tuple.WorkerURL] = nb
			urls = append(urls, tuple.WorkerURL)
		}
		extraMap := cloneJSONMap(tuple.Extra)
		if _, exists := extraMap["interaction_mode"]; !exists {
			extraMap["interaction_mode"] = tuple.InteractionMode
		}
		extraRaw, err := marshalRawObject(extraMap)
		if err != nil {
			return nil, err
		}
		key := capKey{
			name:  tuple.CapabilityID,
			extra: string(extraRaw),
		}
		cb, ok := nb.caps[key]
		if !ok {
			cb = &capBuilder{
				name:     tuple.CapabilityID,
				workUnit: tuple.WorkUnit.Name,
				extra:    extraRaw,
			}
			nb.caps[key] = cb
			nb.order = append(nb.order, key)
		}
		constraintsRaw, err := marshalRawObject(tuple.Constraints)
		if err != nil {
			return nil, err
		}
		cb.offers = append(cb.offers, Offering{
			ID:                  tuple.OfferingID,
			PricePerWorkUnitWei: tuple.PricePerUnitWei,
			Constraints:         constraintsRaw,
		})
	}

	sort.Strings(urls)
	out := make([]Node, 0, len(urls))
	for i, workerURL := range urls {
		nb := nodesByURL[workerURL]
		sort.Slice(nb.order, func(i, j int) bool {
			if nb.order[i].name != nb.order[j].name {
				return nb.order[i].name < nb.order[j].name
			}
			return nb.order[i].extra < nb.order[j].extra
		})
		caps := make([]Capability, 0, len(nb.order))
		for _, key := range nb.order {
			cb := nb.caps[key]
			sort.Slice(cb.offers, func(i, j int) bool {
				if cb.offers[i].ID != cb.offers[j].ID {
					return cb.offers[i].ID < cb.offers[j].ID
				}
				return string(cb.offers[i].Constraints) < string(cb.offers[j].Constraints)
			})
			caps = append(caps, Capability{
				Name:      cb.name,
				WorkUnit:  cb.workUnit,
				Offerings: cb.offers,
				Extra:     cb.extra,
			})
		}
		out = append(out, Node{
			ID:           fmt.Sprintf("node-%d", i+1),
			URL:          workerURL,
			Capabilities: caps,
		})
	}

	return &Manifest{
		SchemaVersion: sm.Manifest.SpecVersion,
		EthAddress:    sm.Manifest.Orch.EthAddress,
		IssuedAt:      sm.Manifest.IssuedAt,
		Nodes:         out,
		Signature: Signature{
			Alg:   sm.Signature.Algorithm,
			Value: sm.Signature.Value,
		},
	}, nil
}

func cloneJSONMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func marshalRawObject(v map[string]any) (json.RawMessage, error) {
	if len(v) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal opaque object: %w", err)
	}
	return json.RawMessage(b), nil
}
