// Package canonical produces deterministic byte representations of
// JSON values. The procedure:
//
//  1. Marshal the input value to JSON via Go's encoding/json.
//  2. Decode the result into a generic any with UseNumber so number
//     precision in opaque blobs survives the round-trip.
//  3. Re-emit the tree with object keys sorted lexicographically and
//     no whitespace.
//
// The output is stable across Go versions because we sort ourselves
// rather than depending on map-iteration order.
//
// The signed manifest payload is the only domain-specific input the
// secure-orch tooling canonicalizes today, but the algorithm is
// type-agnostic — anything Go's json package will marshal works.
package canonical

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

// Bytes returns the canonical JSON byte representation of v.
func Bytes(v any) ([]byte, error) {
	intermediate, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonical: marshal: %w", err)
	}
	return BytesFromJSON(intermediate)
}

// BytesFromJSON canonicalizes already-marshaled JSON bytes. Useful when
// callers hold JSON received from the wire (e.g. an inbox file) and
// want bytes-identical behavior with the marshal-then-canonicalize
// path used at sign time.
func BytesFromJSON(raw []byte) ([]byte, error) {
	var anyVal any
	dec := json.NewDecoder(bytes.NewReader(raw))
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

// SHA256Hex returns 0x-prefixed lower-case hex of sha256(canonical).
// Useful as a diagnostic / display value alongside an Ethereum
// signature.
func SHA256Hex(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "0x" + lowerHex(sum[:])
}

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

func lowerHex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = digits[c>>4]
		out[i*2+1] = digits[c&0x0f]
	}
	return string(out)
}
