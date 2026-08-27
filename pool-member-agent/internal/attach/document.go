// Package attach builds the runner attach document this agent sends a
// broker (livepeer-network-protocol/protocols/runner-attach.md).
//
// The agent is the only party that knows what the host actually runs, so
// it is the only party that fills this in. The operator never types a
// runner fact: they declare "this container, this profile, this model"
// and the profile expands to the capability entry the contract wants.
//
// Everything here is wire shape. The broker validates it (§4) and
// answers with a register_result naming any field it rejected; the agent
// logs that verbatim, because the disagreeing field IS the operator's
// feedback loop.
package attach

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ContractVersion is the attach-contract version this agent speaks.
const ContractVersion = "1.0"

// Document is the attach document (runner-attach §3).
type Document struct {
	ContractVersion string       `json:"contract_version"`
	Credential      Credential   `json:"credential"`
	HostID          string       `json:"host_id"`
	AgentVersion    string       `json:"agent_version"`
	Hardware        []Hardware   `json:"hardware"`
	Capabilities    []Capability `json:"capabilities"`
}

// Credential binds the document to one host enrollment (§3.1.1).
type Credential struct {
	Kind  string `json:"kind"`
	Token string `json:"token,omitempty"`
}

// Hardware is one GPU (§3.1).
type Hardware struct {
	GPUUUID   string            `json:"gpu_uuid"`
	GPUModel  string            `json:"gpu_model"`
	VRAMBytes uint64            `json:"vram_bytes"`
	Driver    string            `json:"driver,omitempty"`
	CUDA      string            `json:"cuda,omitempty"`
	Facts     map[string]string `json:"facts,omitempty"`
}

// Capability is one capability entry (§3.2). Extensions are merged in at
// marshal time so `x-*` keys sit at the entry's top level, where the
// contract puts them.
type Capability struct {
	CapabilityID        string            `json:"capability_id"`
	Protocol            string            `json:"protocol"`
	LocalID             string            `json:"local_id"`
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
	Devices             []string          `json:"devices,omitempty"`

	// Extensions are runner-authored `x-*` keys. Relayed verbatim by the
	// broker; promoted into an offer's advertised extra only when that
	// offer lists the key in extra_from_runner (§3.3).
	Extensions map[string]any `json:"-"`
}

type WorkUnit struct {
	Name      string         `json:"name"`
	Extractor map[string]any `json:"extractor,omitempty"`
}

type Readiness struct {
	Type   string         `json:"type"`
	Path   string         `json:"path,omitempty"`
	Config map[string]any `json:"config,omitempty"`
}

type Heartbeat struct {
	IntervalSeconds int `json:"interval_seconds,omitempty"`
}

type Requirements struct {
	GPUVRAMMinBytes uint64   `json:"gpu_vram_min_bytes,omitempty"`
	GPUModels       []string `json:"gpu_models,omitempty"`
}

// MarshalJSON flattens Extensions into the entry object. Encoding the
// struct and the map separately and merging is the only way to get
// `x-*` siblings of the declared fields without hand-writing the whole
// encoder.
func (c Capability) MarshalJSON() ([]byte, error) {
	type plain Capability // no recursion
	base, err := json.Marshal(plain(c))
	if err != nil {
		return nil, err
	}
	if len(c.Extensions) == 0 {
		return base, nil
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	for k, v := range c.Extensions {
		if !strings.HasPrefix(k, "x-") {
			return nil, fmt.Errorf("attach: extension key %q must start with x-", k)
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("attach: extension %s: %w", k, err)
		}
		merged[k] = raw
	}
	return json.Marshal(merged)
}

// Runner is what an operator (or the pool controller, generating this
// file) declares about one local container. It is deliberately small:
// where it runs, which profile it is, and what it loaded.
type Runner struct {
	// LocalID is the agent's routing key; the broker echoes it on every
	// dispatched request as Livepeer-Runner-Local-Id (§7).
	LocalID string `json:"local_id"`
	// Profile expands to a capability entry: openai-compatible | transcode.
	Profile string `json:"profile"`
	// URL is the container's base URL on the host network. Never sent to
	// the broker — the tunnel is the only way in.
	URL string `json:"url"`
	// CapabilityID overrides the profile's default; for openai-compatible
	// it also selects the endpoint family (chat, embeddings, audio, …).
	CapabilityID string `json:"capability_id,omitempty"`
	// Model is the loaded model, the fact an offer's match selects on.
	Model string `json:"model,omitempty"`
	// Provider labels the serving stack (vllm, ollama, …), advisory.
	Provider string `json:"provider,omitempty"`
	// Devices are the gpu_uuids backing this runner.
	Devices []string `json:"devices,omitempty"`
	// Requirements let a runner state what its host must have; the
	// broker rejects the entry when this host cannot satisfy it (§4.2).
	Requirements *Requirements `json:"requirements,omitempty"`
	// Extensions are `x-*` keys relayed verbatim.
	Extensions map[string]any `json:"extensions,omitempty"`
}

// Host is the non-runner half of the document.
type Host struct {
	HostID       string
	AgentVersion string
	Credential   Credential
	Hardware     []Hardware
}

var localIDRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// Build expands runner declarations into a document. It fails on
// anything the contract would reject as a whole-document error
// (duplicate local ids, a missing credential), because a document the
// broker will refuse outright is a bug to fix here, not traffic to
// send. Per-capability problems are left to the broker: it is the
// authority on which extractors and probes it implements, and a wrong
// guess here would silently drop a capability the broker would have
// accepted.
func Build(host Host, runners []Runner) (*Document, error) {
	if host.HostID == "" {
		return nil, fmt.Errorf("attach: host_id is required")
	}
	if host.Credential.Token == "" {
		return nil, fmt.Errorf("attach: no attach credential — the bundle must supply one")
	}
	if host.Credential.Kind == "" {
		host.Credential.Kind = "bearer"
	}
	doc := &Document{
		ContractVersion: ContractVersion,
		Credential:      host.Credential,
		HostID:          host.HostID,
		AgentVersion:    host.AgentVersion,
		Hardware:        host.Hardware,
		Capabilities:    []Capability{},
	}
	if doc.Hardware == nil {
		doc.Hardware = []Hardware{}
	}
	seen := map[string]bool{}
	for i, r := range runners {
		if r.LocalID == "" {
			r.LocalID = fmt.Sprintf("runner-%d", i)
		}
		if !localIDRE.MatchString(r.LocalID) {
			return nil, fmt.Errorf("attach: runner %d: local_id %q must match %s", i, r.LocalID, localIDRE)
		}
		if seen[r.LocalID] {
			return nil, fmt.Errorf("attach: duplicate local_id %q", r.LocalID)
		}
		seen[r.LocalID] = true
		cap, err := expand(r)
		if err != nil {
			return nil, fmt.Errorf("attach: runner %q: %w", r.LocalID, err)
		}
		// Only claim devices this host actually reported: a device_unknown
		// rejection is avoidable here and confusing there.
		cap.Devices = intersectDevices(r.Devices, doc.Hardware)
		doc.Capabilities = append(doc.Capabilities, *cap)
	}
	return doc, nil
}

func intersectDevices(want []string, hw []Hardware) []string {
	if len(want) == 0 {
		return nil
	}
	have := map[string]bool{}
	for _, h := range hw {
		have[h.GPUUUID] = true
	}
	var out []string
	for _, d := range want {
		if have[d] {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// RouteTable maps local_id → the runner's base URL, for dispatch.
func RouteTable(runners []Runner) map[string]string {
	out := make(map[string]string, len(runners))
	for i, r := range runners {
		id := r.LocalID
		if id == "" {
			id = fmt.Sprintf("runner-%d", i)
		}
		out[id] = strings.TrimRight(r.URL, "/")
	}
	return out
}
