package types

// Boundary validation primitives shared by the manifest decoder.
//
// The daemon used to carry its own v3.0.1 manifest schema and a decoder
// for it, alongside the orch-coordinator envelope. Two manifest shapes
// meant two validators, two canonicalizations, and two things to keep
// equal to the spec; the protocol module's envelope is the only manifest
// now (plan 0043 decision 8), so what survives here are the primitives
// its decoder uses.

import (
	"encoding/hex"
	"errors"
	"strings"
)

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
