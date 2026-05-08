package types

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DecodeManifest parses raw JSON bytes into a *Manifest, applying full
// boundary validation. This is the only entry point for untrusted
// manifest bytes — all internal code paths take a *Manifest after this
// has succeeded.
//
// Validation order matches docs/design-docs/manifest-schema.md §
// "Validation order". Returns *ManifestValidationError wrapping a
// stable sentinel from errors.go on failure.
func DecodeManifest(raw []byte) (*Manifest, error) {
	if len(raw) == 0 {
		return nil, NewValidation(ErrParse, "", "empty body")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields() // strictest at the boundary; manifests with unknown top-level fields are rejected
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, wrapDecodeError(err)
	}
	if dec.More() {
		return nil, NewValidation(ErrParse, "", "trailing data after manifest object")
	}

	if err := validateManifest(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

func validateManifest(m *Manifest) error {
	if err := validateSchemaVersion(m.SchemaVersion); err != nil {
		return NewValidation(ErrInvalidSchemaVersion, "schema_version", err.Error())
	}

	// 4. eth_address
	if _, err := ParseEthAddress(m.EthAddress); err != nil {
		return NewValidation(ErrInvalidEthAddress, "eth_address", err.Error())
	}
	// Reject mixed-case to keep canonical-bytes deterministic across
	// publishers that re-emit the manifest.
	if hasUpperHex(m.EthAddress[2:]) {
		return NewValidation(ErrInvalidEthAddress, "eth_address", "must be lower-cased hex")
	}

	// issued_at
	if m.IssuedAt.IsZero() {
		return NewValidation(ErrParse, "issued_at", "missing")
	}
	// 5. nodes non-empty
	if len(m.Nodes) == 0 {
		return NewValidation(ErrEmptyNodes, "nodes", "must contain at least one node")
	}
	seenIDs := make(map[string]struct{}, len(m.Nodes))
	for i, n := range m.Nodes {
		if n.ID == "" {
			return NewValidation(ErrParse, fmt.Sprintf("nodes[%d].id", i), "missing")
		}
		if _, dup := seenIDs[n.ID]; dup {
			return NewValidation(ErrParse, fmt.Sprintf("nodes[%d].id", i), "duplicate id "+n.ID)
		}
		seenIDs[n.ID] = struct{}{}

		// 6. each node URL valid
		if err := validateNodeURL(n.URL); err != nil {
			return NewValidation(ErrInvalidNodeURL, fmt.Sprintf("nodes[%d].url", i), err.Error())
		}
		if n.WorkerEthAddress != "" {
			if _, err := ParseEthAddress(n.WorkerEthAddress); err != nil {
				return NewValidation(ErrInvalidEthAddress, fmt.Sprintf("nodes[%d].worker_eth_address", i), err.Error())
			}
			if hasUpperHex(n.WorkerEthAddress[2:]) {
				return NewValidation(ErrInvalidEthAddress, fmt.Sprintf("nodes[%d].worker_eth_address", i), "must be lower-cased hex")
			}
		}
		if err := validateOpaqueJSONObject(fmt.Sprintf("nodes[%d].extra", i), n.Extra); err != nil {
			return err
		}

		// Capabilities: each must have a name; opaque otherwise.
		for j, c := range n.Capabilities {
			if c.Name == "" {
				return NewValidation(ErrParse, fmt.Sprintf("nodes[%d].capabilities[%d].name", i, j), "missing")
			}
			if err := validateOpaqueJSONObject(fmt.Sprintf("nodes[%d].capabilities[%d].extra", i, j), c.Extra); err != nil {
				return err
			}
			for k, off := range c.Offerings {
				if off.ID == "" {
					return NewValidation(ErrParse,
						fmt.Sprintf("nodes[%d].capabilities[%d].offerings[%d].id", i, j, k),
						"missing",
					)
				}
				if off.PricePerWorkUnitWei != "" && !isDecimalNonNegInt(off.PricePerWorkUnitWei) {
					return NewValidation(ErrParse,
						fmt.Sprintf("nodes[%d].capabilities[%d].offerings[%d].price_per_work_unit_wei", i, j, k),
						"must be non-negative decimal integer string",
					)
				}
				if err := validateOpaqueJSONObject(fmt.Sprintf("nodes[%d].capabilities[%d].offerings[%d].constraints", i, j, k), off.Constraints); err != nil {
					return err
				}
			}
		}
	}

	// 7-8. signature shape
	if m.Signature.Alg != SignatureAlgEthPersonal {
		return NewValidation(ErrSignatureMalformed, "signature.alg",
			fmt.Sprintf("expected %q, got %q", SignatureAlgEthPersonal, m.Signature.Alg))
	}
	if !isHex0x(m.Signature.Value, 65) {
		return NewValidation(ErrSignatureMalformed, "signature.value",
			"expected 0x-prefixed 130-hex (65 bytes)")
	}
	if m.Signature.SignedCanonicalBytesSHA256 != "" && !isHex0x(m.Signature.SignedCanonicalBytesSHA256, 32) {
		return NewValidation(ErrSignatureMalformed, "signature.signed_canonical_bytes_sha256",
			"expected 0x-prefixed 64-hex (32 bytes)")
	}
	return nil
}

func validateNodeURL(s string) error {
	if s == "" {
		return errors.New("missing")
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return errors.New("not a parseable URL")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		// permit localhost for dev-mode operators
		if strings.HasPrefix(u.Host, "localhost") || strings.HasPrefix(u.Host, "127.0.0.1") {
			return nil
		}
		return errors.New("http:// only permitted for localhost")
	default:
		return errors.New("scheme must be https (or http for localhost)")
	}
}

func hasUpperHex(s string) bool {
	for _, r := range s {
		if r >= 'A' && r <= 'F' {
			return true
		}
	}
	return false
}

func isDecimalNonNegInt(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isHex0x(s string, byteLen int) bool {
	if !strings.HasPrefix(s, "0x") {
		return false
	}
	body := s[2:]
	if len(body) != byteLen*2 {
		return false
	}
	_, err := hex.DecodeString(body)
	return err == nil
}

// DecodeUnsignedManifest parses a manifest body that has NOT yet been
// signed. It enforces every validation DecodeManifest does EXCEPT the
// signature shape and recovery checks — those are the publisher's job
// to fill in next.
//
// This is the only relaxed JSON entry point in the codebase. Any other
// json.Unmarshal of bytes into *Manifest is flagged by the
// no-unverified-manifest lint as a boundary-bypass bug.
func DecodeUnsignedManifest(raw []byte) (*Manifest, error) {
	if len(raw) == 0 {
		return nil, NewValidation(ErrParse, "", "empty body")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, wrapDecodeError(err)
	}
	if dec.More() {
		return nil, NewValidation(ErrParse, "", "trailing data after manifest object")
	}
	if err := validateUnsignedManifest(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// validateUnsignedManifest is validateManifest minus the signature
// checks. We deliberately keep the implementation simple by inlining
// the shared checks rather than refactoring validateManifest, because
// the signature checks are interleaved with the rest and a partial
// extraction would be more error-prone than a single duplication.
func validateUnsignedManifest(m *Manifest) error {
	if err := validateSchemaVersion(m.SchemaVersion); err != nil {
		return NewValidation(ErrInvalidSchemaVersion, "schema_version", err.Error())
	}
	if _, err := ParseEthAddress(m.EthAddress); err != nil {
		return NewValidation(ErrInvalidEthAddress, "eth_address", err.Error())
	}
	if hasUpperHex(m.EthAddress[2:]) {
		return NewValidation(ErrInvalidEthAddress, "eth_address", "must be lower-cased hex")
	}
	if len(m.Nodes) == 0 {
		return NewValidation(ErrEmptyNodes, "nodes", "must contain at least one node")
	}
	seenIDs := make(map[string]struct{}, len(m.Nodes))
	for i, n := range m.Nodes {
		if n.ID == "" {
			return NewValidation(ErrParse, fmt.Sprintf("nodes[%d].id", i), "missing")
		}
		if _, dup := seenIDs[n.ID]; dup {
			return NewValidation(ErrParse, fmt.Sprintf("nodes[%d].id", i), "duplicate id "+n.ID)
		}
		seenIDs[n.ID] = struct{}{}
		if err := validateNodeURL(n.URL); err != nil {
			return NewValidation(ErrInvalidNodeURL, fmt.Sprintf("nodes[%d].url", i), err.Error())
		}
		if n.WorkerEthAddress != "" {
			if _, err := ParseEthAddress(n.WorkerEthAddress); err != nil {
				return NewValidation(ErrInvalidEthAddress, fmt.Sprintf("nodes[%d].worker_eth_address", i), err.Error())
			}
			if hasUpperHex(n.WorkerEthAddress[2:]) {
				return NewValidation(ErrInvalidEthAddress, fmt.Sprintf("nodes[%d].worker_eth_address", i), "must be lower-cased hex")
			}
		}
		if err := validateOpaqueJSONObject(fmt.Sprintf("nodes[%d].extra", i), n.Extra); err != nil {
			return err
		}
		for j, c := range n.Capabilities {
			if c.Name == "" {
				return NewValidation(ErrParse, fmt.Sprintf("nodes[%d].capabilities[%d].name", i, j), "missing")
			}
			if err := validateOpaqueJSONObject(fmt.Sprintf("nodes[%d].capabilities[%d].extra", i, j), c.Extra); err != nil {
				return err
			}
			for k, off := range c.Offerings {
				if off.ID == "" {
					return NewValidation(ErrParse,
						fmt.Sprintf("nodes[%d].capabilities[%d].offerings[%d].id", i, j, k),
						"missing",
					)
				}
				if off.PricePerWorkUnitWei != "" && !isDecimalNonNegInt(off.PricePerWorkUnitWei) {
					return NewValidation(ErrParse,
						fmt.Sprintf("nodes[%d].capabilities[%d].offerings[%d].price_per_work_unit_wei", i, j, k),
						"must be non-negative decimal integer string",
					)
				}
				if err := validateOpaqueJSONObject(fmt.Sprintf("nodes[%d].capabilities[%d].offerings[%d].constraints", i, j, k), off.Constraints); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func wrapDecodeError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "unknown field") {
		field := msg
		if idx := strings.Index(msg, "\""); idx >= 0 {
			if end := strings.LastIndex(msg, "\""); end > idx {
				field = msg[idx+1 : end]
			}
		}
		return NewValidation(ErrUnknownField, field, "unknown_field at "+field)
	}
	return NewValidation(ErrParse, "", msg)
}

func validateSchemaVersion(v string) error {
	if v == "" {
		return errors.New(`missing; expected "^3.0.1"`)
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return fmt.Errorf(`expected semver string in range "^3.0.1" (got %q)`, v)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return fmt.Errorf(`expected semver string in range "^3.0.1" (got %q)`, v)
		}
		nums[i] = n
	}
	if nums[0] != 3 {
		return fmt.Errorf(`expected semver string in range "^3.0.1" (got %q)`, v)
	}
	if nums[1] == 0 && nums[2] < 1 {
		return fmt.Errorf(`expected semver string in range "^3.0.1" (got %q)`, v)
	}
	return nil
}

func validateOpaqueJSONObject(path string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return NewValidation(ErrParse, path, err.Error())
	}
	if dec.More() {
		return NewValidation(ErrParse, path, "trailing data after object")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return NewValidation(ErrParse, path, "must be a JSON object")
	}
	if err := validateJSONDepth(obj, 1); err != nil {
		return NewValidation(ErrParse, path, err.Error())
	}
	return nil
}

func validateJSONDepth(v any, depth int) error {
	if depth > 10 {
		return errors.New("max nesting depth is 10")
	}
	switch x := v.(type) {
	case map[string]any:
		for _, child := range x {
			if err := validateJSONDepth(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range x {
			if err := validateJSONDepth(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// MustEncodeManifest is the inverse of DecodeManifest for diagnostic
// purposes (writing a manifest to disk for the operator's HTTP server).
// It produces minified JSON with key order following the Manifest
// struct definition (NOT canonical-keys-sorted — that's CanonicalBytes).
func MustEncodeManifest(m *Manifest) []byte {
	b, err := json.Marshal(m)
	if err != nil {
		// json.Marshal of a well-formed Manifest cannot fail.
		panic(fmt.Sprintf("MustEncodeManifest: %v", err))
	}
	return b
}

// EncodeManifestPretty produces indented JSON; useful when writing the
// manifest file for human inspection. The publisher uses this so an
// operator who curl-fetches the manifest sees readable JSON.
func EncodeManifestPretty(m *Manifest) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

// IssuedAtRFC3339 is a small adapter that emits / parses RFC3339-UTC,
// since the JSON decoder accepts other forms by default.
func IssuedAtRFC3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }
