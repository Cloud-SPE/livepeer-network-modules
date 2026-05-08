package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// CanonicalBytes returns the deterministic byte representation of the
// manifest used as input to the signature digest. The procedure:
//
//  1. Clone the manifest and zero its Signature field.
//  2. Marshal to JSON via Go's encoding/json (which produces field
//     order following struct-tag order).
//  3. Re-walk the resulting JSON tree, emitting it in a key-sorted,
//     whitespace-free form.
//
// The output is stable across Go versions because we do the sorting
// ourselves rather than depending on map-iteration order.
func CanonicalBytes(m *Manifest) ([]byte, error) {
	c := m.Clone()
	c.Signature = Signature{} // zero — but key remains in JSON
	c.IssuedAt = time.Time{}
	for i := range c.Nodes {
		c.Nodes[i].WorkerEthAddress = ""
	}

	// First marshal — produces deterministic key order for *struct* fields.
	intermediate, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("canonical: marshal: %w", err)
	}

	// Decode into a generic value so we can re-encode with sorted map keys
	// (this also normalizes any nested maps inside Capability.Extra and
	// Model.Constraints, which are arbitrary JSON).
	var anyVal any
	dec := json.NewDecoder(bytes.NewReader(intermediate))
	dec.UseNumber() // preserve number precision in opaque blobs
	if err := dec.Decode(&anyVal); err != nil {
		return nil, fmt.Errorf("canonical: decode: %w", err)
	}

	var buf bytes.Buffer
	if err := writeCanonical(&buf, anyVal); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CanonicalSHA256 returns 0x-prefixed lower-case hex of sha256(canonical).
// Useful as the SignedCanonicalBytesSHA256 diagnostic field.
func CanonicalSHA256(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "0x" + lowerHex(sum[:])
}

// writeCanonical writes a JSON-decoded value in canonical form: object
// keys sorted lexicographically, no whitespace, numbers in their
// json.Number string form.
func writeCanonical(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		b, err := json.Marshal(x)
		if err != nil {
			return err
		}
		buf.Write(b)
	case json.Number:
		buf.WriteString(string(x))
	case float64:
		// json.Decoder with UseNumber should not produce bare float64,
		// but be defensive: encode via Marshal (canonical fp form).
		b, err := json.Marshal(x)
		if err != nil {
			return err
		}
		buf.Write(b)
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeCanonical(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("canonical: unexpected type %T", v)
	}
	return nil
}

// lowerHex is a tiny zero-alloc lower-case hex encoder. Renamed from
// `hex` to avoid clashing with stdlib encoding/hex elsewhere in the
// package.
func lowerHex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = digits[c>>4]
		out[i*2+1] = digits[c&0x0f]
	}
	return string(out)
}
