package attach

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// The runner self-description contract (plan 0045 §3;
// livepeer-network-protocol/protocols/runner-contract.md).
//
// A runner says what it is. It serves its own capability entry — every
// field of runner-attach §3.2 the runner owns — at ContractPath, and the
// agent reads it once at attach and relays it. The agent adds only what
// the HOST knows: which container this is (local_id), which of the
// host's GPUs back it (devices), and whether the pool is withdrawing it
// (draining).
//
// There is no other mechanism. The adapter profiles that used to expand
// "this container, this profile, this model" into a capability entry
// are gone: they put runner facts in the agent, where changing one meant
// shipping a new agent to every member. A runner that does not serve
// its contract cannot attach, and the failure names it.

// ContractPath is where a runner serves its contract.
const ContractPath = "/.well-known/livepeer-runner"

// maxContractBytes bounds a contract body. runner-attach caps x-* at
// 32 KiB per capability and session_params_schema at 16 KiB, so a
// contract that needs more than this is not describing a runner.
const maxContractBytes = 128 << 10

// Contract is the runner-owned half of a capability entry. Field for
// field it is runner-attach §3.2 minus local_id, devices and draining,
// which the host supplies.
type Contract struct {
	CapabilityID        string            `json:"capability_id"`
	Protocol            string            `json:"protocol"`
	Transports          []string          `json:"transports,omitempty"`
	DescriptorSchemas   []string          `json:"descriptor_schemas,omitempty"`
	WorkUnit            WorkUnit          `json:"work_unit"`
	Paths               map[string]string `json:"paths"`
	Readiness           Readiness         `json:"readiness"`
	Identity            map[string]string `json:"identity"`
	SchemaVersions      map[string]string `json:"schema_versions"`
	Metering            string            `json:"metering,omitempty"`
	Heartbeat           *Heartbeat        `json:"heartbeat,omitempty"`
	SessionParamsSchema json.RawMessage   `json:"session_params_schema,omitempty"`
	Requirements        *Requirements     `json:"requirements,omitempty"`

	// Extensions holds every top-level x-* key, relayed verbatim.
	Extensions map[string]any `json:"-"`
}

// contractFields is the closed set of non-extension keys. An unknown
// key is a typo in the runner's contract, and a typo that decoded to
// nothing would attach a runner that is not what it says it is.
var contractFields = map[string]bool{
	"capability_id": true, "protocol": true, "transports": true, "descriptor_schemas": true,
	"work_unit": true, "paths": true, "readiness": true, "identity": true, "schema_versions": true,
	"metering": true, "heartbeat": true, "session_params_schema": true, "requirements": true,
}

// UnmarshalJSON decodes the known fields strictly and gathers x-* keys.
func (c *Contract) UnmarshalJSON(raw []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return err
	}
	type plain Contract
	var p plain
	known := make(map[string]json.RawMessage, len(top))
	ext := map[string]any{}
	for k, v := range top {
		switch {
		case contractFields[k]:
			known[k] = v
		case strings.HasPrefix(k, "x-"):
			var val any
			if err := json.Unmarshal(v, &val); err != nil {
				return fmt.Errorf("extension %s: %w", k, err)
			}
			ext[k] = val
		default:
			return fmt.Errorf("unknown field %q (runner-attach §3.2 names the fields; extensions must start with x-)", k)
		}
	}
	kb, err := json.Marshal(known)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(kb, &p); err != nil {
		return err
	}
	*c = Contract(p)
	if len(ext) > 0 {
		c.Extensions = ext
	}
	return nil
}

// Validate is the agent's own check that a contract could possibly be a
// capability entry. It is deliberately shallow: the broker validates the
// document in full (runner-attach §4) and names the field it rejects,
// and repeating that here would be a second validator to keep in step.
// What the agent refuses is the contract that cannot even be relayed.
func (c *Contract) Validate() error {
	if c == nil {
		return errors.New("empty contract")
	}
	if strings.TrimSpace(c.CapabilityID) == "" {
		return errors.New("capability_id is required")
	}
	if strings.TrimSpace(c.Protocol) == "" {
		return errors.New("protocol is required")
	}
	if len(c.Paths) == 0 {
		return errors.New("paths is required")
	}
	if strings.TrimSpace(c.WorkUnit.Name) == "" {
		return errors.New("work_unit.name is required")
	}
	if strings.TrimSpace(c.Readiness.Type) == "" {
		return errors.New("readiness.type is required")
	}
	return nil
}

// Fetch reads a runner's contract from its base URL.
// Fetch reads the contract(s) a runner serves. The body is one entry,
// or an array of entries for a container that serves more than one
// capability — the audio runner's transcriptions and translations, a
// vendor-backed runner with several models. Each element is validated
// as a contract of its own.
func Fetch(ctx context.Context, client *http.Client, baseURL string) ([]*Contract, error) {
	if client == nil {
		client = http.DefaultClient
	}
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, errors.New("runner has no url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+ContractPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s%s: %w", base, ContractPath, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s%s returned %d; a runner must serve its contract there", base, ContractPath, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxContractBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read contract: %w", err)
	}
	if len(body) > maxContractBytes {
		return nil, fmt.Errorf("contract exceeds %d bytes", maxContractBytes)
	}
	trimmed := bytes.TrimSpace(body)
	var raws []json.RawMessage
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &raws); err != nil {
			return nil, fmt.Errorf("contract array is not valid: %w", err)
		}
		if len(raws) == 0 {
			return nil, errors.New("contract array is empty; a runner that serves nothing has nothing to attach")
		}
	} else {
		raws = []json.RawMessage{trimmed}
	}
	out := make([]*Contract, 0, len(raws))
	seen := map[string]bool{}
	for i, raw := range raws {
		var c Contract
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("contract[%d] is not valid: %w", i, err)
		}
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("contract[%d] is not valid: %w", i, err)
		}
		// The broker's own uniqueness rule (runner-attach §4.1): one
		// capability MAY appear under several identities — a chat proxy
		// serving three models is three entries — never twice under one.
		// Two identical entries from one container would be the same
		// runner twice under two local ids, dispatched to as two cards.
		key := c.CapabilityID + "|" + identityKey(c.Identity)
		if seen[key] {
			return nil, fmt.Errorf("contract[%d] repeats capability_id %q with the same identity; one entry per (capability, identity)", i, c.CapabilityID)
		}
		seen[key] = true
		out = append(out, &c)
	}
	return out, nil
}

// LocalIDFor is the local id of the n-th contract entry a runner
// serves: the runner's own id for the first, and <id>.<n> after it, so
// a second capability from one container is a distinct runner to the
// broker (runner-attach §3.2: local_id is unique per document) while
// the agent can still route it — BaseLocalID recovers the container.
func LocalIDFor(localID string, n int) string {
	if n == 0 {
		return localID
	}
	return fmt.Sprintf("%s.%d", localID, n)
}

// BaseLocalID strips the .<n> suffix LocalIDFor adds; the result names
// the configured container.
func BaseLocalID(localID string) string {
	if i := strings.LastIndexByte(localID, '.'); i > 0 {
		if _, err := strconv.Atoi(localID[i+1:]); err == nil {
			return localID[:i]
		}
	}
	return localID
}

// Resolved is one runner the agent can attach: the host's facts plus
// what the runner said about itself.
type Resolved struct {
	Runner   Runner
	Contract *Contract
}

// ResolveError names a runner that could not be resolved. The message
// is written for the operator who has to fix it: it carries the
// container, the URL, and what was expected there.
type ResolveError struct {
	LocalID string
	URL     string
	Err     error
}

func (e *ResolveError) Error() string {
	return fmt.Sprintf("runner %q at %s has no usable contract: %v — it cannot attach until it serves GET %s (runner-contract.md)",
		e.LocalID, e.URL, e.Err, ContractPath)
}

func (e *ResolveError) Unwrap() error { return e.Err }

// Resolve fetches every runner's contract. A runner that has none is
// reported and OMITTED, not fatal: one image that does not adhere must
// not keep the rest of the host from attaching. The errors are the
// impacted-runner inventory — generated, not hand-maintained.
func Resolve(ctx context.Context, client *http.Client, runners []Runner) ([]Resolved, []error) {
	out := make([]Resolved, 0, len(runners))
	var errs []error
	for i, r := range runners {
		if r.LocalID == "" {
			r.LocalID = fmt.Sprintf("runner-%d", i)
		}
		cs, err := Fetch(ctx, client, r.URL)
		if err != nil {
			errs = append(errs, &ResolveError{LocalID: r.LocalID, URL: r.URL, Err: err})
			continue
		}
		for n, c := range cs {
			entry := r
			entry.LocalID = LocalIDFor(r.LocalID, n)
			out = append(out, Resolved{Runner: entry, Contract: c})
		}
	}
	return out, errs
}

// capabilityOf assembles the wire entry from the two halves.
func capabilityOf(r Runner, c *Contract, hw []Hardware) Capability {
	cap := Capability{
		CapabilityID:        c.CapabilityID,
		Protocol:            c.Protocol,
		LocalID:             r.LocalID,
		Transports:          c.Transports,
		DescriptorSchemas:   c.DescriptorSchemas,
		WorkUnit:            c.WorkUnit,
		Paths:               c.Paths,
		Readiness:           c.Readiness,
		Identity:            c.Identity,
		SchemaVersions:      c.SchemaVersions,
		Metering:            c.Metering,
		Heartbeat:           c.Heartbeat,
		SessionParamsSchema: c.SessionParamsSchema,
		Requirements:        c.Requirements,
		Extensions:          c.Extensions,
		Devices:             intersectDevices(r.Devices, hw),
		Draining:            r.Draining,
	}
	if cap.Identity == nil {
		cap.Identity = map[string]string{}
	}
	if cap.SchemaVersions == nil {
		cap.SchemaVersions = map[string]string{}
	}
	return cap
}

// sortedKeys is for deterministic output where a map's order would
// otherwise leak into a golden.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// identityKey is the key-sorted identity, the broker's duplicate key.
func identityKey(identity map[string]string) string {
	keys := make([]string, 0, len(identity))
	for k := range identity {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + identity[k] + ";")
	}
	return b.String()
}
