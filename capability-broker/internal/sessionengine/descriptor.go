package sessionengine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

// Descriptor validation — runtime-descriptor framework §3, schema-agnostic.
// The broker validates structure only; it never interprets fields.

// DefaultDescriptorMaxBytes is the framework's default size cap for the
// serialized runtime object.
const DefaultDescriptorMaxBytes = 16 * 1024

// maxGrants is the framework cap on admission grants per descriptor.
const maxGrants = 4

var schemaTagRE = regexp.MustCompile(`^[a-z][a-z0-9-]*/v[0-9]+$`)

// Grant is one admission grant as returned by the runner. The Secret is
// delivered to the gateway exactly once at open and never persisted in
// recoverable form.
type Grant struct {
	ID         string    `json:"id"`
	Operations []string  `json:"operations"`
	Secret     string    `json:"secret"`
	ExpiresAt  time.Time `json:"expires_at"`
	MaxUses    int       `json:"max_uses,omitempty"`
}

// Descriptor is the validated runtime descriptor.
type Descriptor struct {
	Schema  string          `json:"schema"`
	Public  json.RawMessage `json:"public"`
	Private json.RawMessage `json:"private,omitempty"`
	Grants  []Grant         `json:"grants,omitempty"`
}

// DescriptorError is a validation failure; opens fail closed on it.
type DescriptorError struct{ Reason string }

func (e *DescriptorError) Error() string { return "descriptor: " + e.Reason }

func descErr(format string, args ...any) error {
	return &DescriptorError{Reason: fmt.Sprintf(format, args...)}
}

// ParseDescriptor validates the raw runtime object against the
// framework rules and the offering's declared schema. maxBytes <= 0
// means DefaultDescriptorMaxBytes.
func ParseDescriptor(raw json.RawMessage, declaredSchema string, maxBytes int) (*Descriptor, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultDescriptorMaxBytes
	}
	if len(raw) == 0 {
		return nil, descErr("runtime object missing")
	}
	if len(raw) > maxBytes {
		return nil, descErr("runtime object is %d bytes; cap is %d", len(raw), maxBytes)
	}

	// Closed top-level key set: schema, public, private, grants. Any
	// other key is a partition-bypass vector and rejects the open.
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, descErr("runtime is not a JSON object at path $")
	}
	for k := range keys {
		switch k {
		case "schema", "public", "private", "grants":
		default:
			return nil, descErr("unknown top-level key at path $.%s", k)
		}
	}

	var d Descriptor
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, descErr("runtime shape invalid: %v", err)
	}
	if !schemaTagRE.MatchString(d.Schema) {
		return nil, descErr("schema tag %q does not match <name>/v<N>", d.Schema)
	}
	if d.Schema != declaredSchema {
		return nil, descErr("schema %q does not match offering's declared descriptor_schema %q", d.Schema, declaredSchema)
	}
	if !isJSONObject(d.Public) {
		return nil, descErr("public part must be a JSON object at path $.public")
	}
	if len(d.Private) > 0 && !isJSONObject(d.Private) {
		return nil, descErr("private part must be a JSON object at path $.private")
	}
	if len(d.Grants) > maxGrants {
		return nil, descErr("%d grants; cap is %d", len(d.Grants), maxGrants)
	}
	seen := make(map[string]struct{}, len(d.Grants))
	for i, g := range d.Grants {
		if g.ID == "" || g.Secret == "" || len(g.Operations) == 0 || g.ExpiresAt.IsZero() {
			return nil, descErr("grant[%d] missing required field (id, operations, secret, expires_at)", i)
		}
		if g.MaxUses < 0 {
			return nil, descErr("grant[%d] max_uses must be positive when present", i)
		}
		if _, dup := seen[g.ID]; dup {
			return nil, descErr("grant id %q duplicated", g.ID)
		}
		seen[g.ID] = struct{}{}
	}
	return &d, nil
}

func isJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var probe map[string]json.RawMessage
	return json.Unmarshal(raw, &probe) == nil
}
