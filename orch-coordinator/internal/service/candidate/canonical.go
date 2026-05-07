// Package candidate builds the candidate manifest from the scrape
// cache. The output is byte-identical given the same input — JCS
// canonicalization (RFC 8785), deterministic tuple ordering, scrape-
// window-end timestamps. The cold key on secure-orch signs the
// manifest.json bytes byte-for-byte; the operator-only metadata
// sidecar carries provenance that must NOT enter the signed bytes.
//
// Aggregation rules per plan 0018 §5 / Q2:
//
//   - Uniqueness key: (capability_id, offering_id, extra, constraints).
//   - Identical key + different prices → hard-fail loud.
//   - Identical key + identical price + different worker_url →
//     emit one tuple with the lex-min worker_url; alternates go in
//     the metadata sidecar.
//   - Different extra/constraints → distinct identities; emit both.
package candidate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// CanonicalBytes is the JCS-equivalent serialization the manifest
// signature is computed over. The procedure mirrors
// secure-orch-console/internal/canonical/canonical.go (zero-dep
// stdlib only): marshal → re-decode with UseNumber → re-emit with
// keys sorted lexicographically and no whitespace.
func CanonicalBytes(v any) ([]byte, error) {
	intermediate, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonical: marshal: %w", err)
	}
	return CanonicalBytesFromJSON(intermediate)
}

// CanonicalBytesFromJSON canonicalizes already-marshaled JSON bytes.
func CanonicalBytesFromJSON(raw []byte) ([]byte, error) {
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
// Useful as a diagnostic / display value alongside the Ethereum
// signature.
func SHA256Hex(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "0x" + hex.EncodeToString(sum[:])
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
