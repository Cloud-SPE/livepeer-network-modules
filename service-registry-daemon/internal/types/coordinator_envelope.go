package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// CoordinatorSignatureAlg is the current orch-coordinator envelope
// signature algorithm marker. The resolver still verifies the
// signature bytes using Ethereum personal-sign recovery; this string
// only identifies the source envelope contract.
const CoordinatorSignatureAlg = "secp256k1"

// settlementKeyRE matches an uncompressed secp256k1 public key in hex.
var settlementKeyRE = regexp.MustCompile(`^0x[0-9a-f]{130}$`)

// CoordinatorSignedManifest is the compatibility envelope currently
// published by orch-coordinator.
type CoordinatorSignedManifest struct {
	Manifest  CoordinatorManifestPayload   `json:"manifest"`
	Signature CoordinatorEnvelopeSignature `json:"signature"`
}

type CoordinatorManifestPayload struct {
	SpecVersion    string          `json:"spec_version"`
	PublicationSeq uint64          `json:"publication_seq"`
	IssuedAt       time.Time       `json:"issued_at"`
	ExpiresAt      time.Time       `json:"expires_at"`
	Orch           CoordinatorOrch `json:"orch"`
	// SettlementKeys are hot keys the cold key delegates settlement
	// signing to. A broker is network-exposed and must never hold the
	// key that anchors the operator's on-chain identity.
	SettlementKeys []CoordinatorSettlementKey `json:"settlement_keys,omitempty"`
	Capabilities   []CoordinatorCapability    `json:"capabilities"`
}

// CoordinatorSettlementKey is one delegated settlement-signing key.
// Consumers accept a settlement signed by any key whose window contains
// the record's issued_at; an outgoing key stays listed until its
// expires_at so a record signed just before a rotation still verifies.
type CoordinatorSettlementKey struct {
	PublicKey string    `json:"public_key"`
	NotBefore time.Time `json:"not_before"`
	ExpiresAt time.Time `json:"expires_at"`
}

type CoordinatorOrch struct {
	EthAddress string `json:"eth_address"`
	ServiceURI string `json:"service_uri,omitempty"`
}

// CoordinatorCapability is one capability tuple of the coordinator
// envelope.
//
// Protocol replaces the removed interaction-mode field (manifest spec
// 1.0.0; see livepeer-network-protocol/protocols/offering-axes.md). The
// declared axes ride alongside it in exactly one of Job (paid-job/*) or
// Session (paid-session/*).
//
// Job and Session are deliberately json.RawMessage: per the "Who
// consumes what" table in offering-axes.md, the registry/resolver layer
// gates on *nothing* here — it is pure pass-through. Keeping the bytes
// verbatim means a new axis (or a whole new protocol) needs no change in
// this daemon, and nothing downstream sees a value this layer reshaped.
type CoordinatorCapability struct {
	CapabilityID    string              `json:"capability_id"`
	OfferingID      string              `json:"offering_id"`
	Protocol        string              `json:"protocol"`
	Job             json.RawMessage     `json:"job,omitempty"`
	Session         json.RawMessage     `json:"session,omitempty"`
	WorkUnit        CoordinatorWorkUnit `json:"work_unit"`
	PricePerUnitWei string              `json:"price_per_unit_wei"`
	// PerUnits is the price denominator (offering-axes.md §6). Absent
	// means 1. The envelope decoder rejects unknown fields, so this must
	// exist here for a manifest that declares it to parse at all.
	PerUnits    uint64         `json:"per_units,omitempty"`
	WorkerURL   string         `json:"worker_url"`
	Extra       map[string]any `json:"extra,omitempty"`
	Constraints map[string]any `json:"constraints,omitempty"`
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
	for i, k := range sm.Manifest.SettlementKeys {
		if !settlementKeyRE.MatchString(k.PublicKey) {
			return NewValidation(ErrParse, fmt.Sprintf("manifest.settlement_keys[%d].public_key", i),
				"must be 0x-prefixed 130-hex (uncompressed secp256k1)")
		}
		if k.NotBefore.IsZero() || k.ExpiresAt.IsZero() {
			return NewValidation(ErrParse, fmt.Sprintf("manifest.settlement_keys[%d]", i),
				"not_before and expires_at are required")
		}
		if !k.ExpiresAt.After(k.NotBefore) {
			return NewValidation(ErrParse, fmt.Sprintf("manifest.settlement_keys[%d]", i),
				"expires_at must be after not_before")
		}
	}
	for i, c := range sm.Manifest.Capabilities {
		if c.CapabilityID == "" {
			return NewValidation(ErrParse, fmt.Sprintf("manifest.capabilities[%d].capability_id", i), "missing")
		}
		if c.OfferingID == "" {
			return NewValidation(ErrParse, fmt.Sprintf("manifest.capabilities[%d].offering_id", i), "missing")
		}
		// Presence only. The protocol tag's grammar, the job/session
		// axes, and the protocol↔axes pairing are all gated by the
		// consumers that actually speak them (clearinghouse, gateway,
		// broker) — never here. See offering-axes.md §4.
		if c.Protocol == "" {
			return NewValidation(ErrParse, fmt.Sprintf("manifest.capabilities[%d].protocol", i), "missing")
		}
		// The declaration is authoritative and `extra` is opaque
		// operator metadata. A tuple that puts a declaration key in
		// extra is either confused or trying to make a consumer read a
		// different protocol than the one it signed; either way the
		// manifest is refused rather than silently corrected.
		for _, reserved := range []string{"protocol", "job", "session"} {
			if _, clash := c.Extra[reserved]; clash {
				return NewValidation(ErrParse,
					fmt.Sprintf("manifest.capabilities[%d].extra.%s", i, reserved),
					"reserved: the signed declaration owns this key")
			}
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
		protocol string
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
		mirrorDeclaration(extraMap, tuple)
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
				protocol: tuple.Protocol,
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
			PerUnits:            tuple.PerUnits,
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
				Protocol:  cb.protocol,
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

	keys := make([]SettlementKey, 0, len(sm.Manifest.SettlementKeys))
	for _, k := range sm.Manifest.SettlementKeys {
		keys = append(keys, SettlementKey{
			PublicKey: k.PublicKey,
			NotBefore: k.NotBefore,
			ExpiresAt: k.ExpiresAt,
		})
	}
	// Newest first, so a consumer taking the head gets the signer for
	// records emitted now.
	sort.Slice(keys, func(i, j int) bool { return keys[i].NotBefore.After(keys[j].NotBefore) })

	return &Manifest{
		SchemaVersion:  sm.Manifest.SpecVersion,
		EthAddress:     sm.Manifest.Orch.EthAddress,
		IssuedAt:       sm.Manifest.IssuedAt,
		Nodes:          out,
		SettlementKeys: keys,
		Signature: Signature{
			Alg:   sm.Signature.Algorithm,
			Value: sm.Signature.Value,
		},
	}, nil
}

// mirrorDeclaration copies the tuple's declared axes into the projected
// capability's opaque extra block, and its protocol alongside them.
//
// The node-oriented projection (Node → Capability → Offering) predates
// the manifest's capability-tuple shape, and the gRPC surface carries a
// capability's axes solely as extra_json. Mirroring here is what keeps
// the declaration reaching consumers at all: gateways select routes on
// session.descriptor_schema and job.transports, so dropping the axes
// while forwarding the rest would silently break route selection.
//
// The signed tuple WINS every collision. An operator that publishes its
// own "protocol", "job" or "session" key in extra has its value
// overwritten here, and DecodeCoordinatorEnvelope rejects the manifest
// outright — see validateCoordinatorEnvelope. The precedence used to run
// the other way, so an orch could publish extra.protocol = "paid-job/v1"
// on a paid-session offering and every downstream consumer that gates on
// protocol would believe it.
//
// The axes stay raw bytes: this layer gates on nothing inside them, and
// a typed mirror would silently drop any axis a later spec minor adds.
func mirrorDeclaration(extra map[string]any, tuple CoordinatorCapability) {
	extra["protocol"] = tuple.Protocol
	if len(tuple.Job) > 0 {
		extra["job"] = tuple.Job
	} else {
		delete(extra, "job")
	}
	if len(tuple.Session) > 0 {
		extra["session"] = tuple.Session
	} else {
		delete(extra, "session")
	}
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
