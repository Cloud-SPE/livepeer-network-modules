// Package attach builds the runner attach document this agent sends a
// broker (livepeer-network-protocol/protocols/runner-attach.md).
//
// The RUNNER is the only party that knows what it is, so it says so:
// it serves its own capability entry (contract.go) and the agent relays
// it, adding only what the host knows — which container, which GPUs,
// whether it is being withdrawn. Nobody types a runner fact anywhere.
//
// Everything here is wire shape. The broker validates it (§4) and
// answers with a register_result naming any field it rejected; the agent
// logs that verbatim, because the disagreeing field IS the operator's
// feedback loop.
package attach

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// ContractVersion is the attach-contract version this agent speaks when
// it uses a 1.2 field; ContractVersionBase is what it sends otherwise.
// runner-attach §8: send the lowest minor that carries the fields used,
// so a 1.1 broker keeps accepting a host that has nothing new to say.
const (
	ContractVersion     = "1.2"
	ContractVersionBase = "1.1"
)

// Document is the attach document (runner-attach §3).
type Document struct {
	ContractVersion string     `json:"contract_version"`
	Credential      Credential `json:"credential"`
	HostID          string     `json:"host_id"`
	AgentVersion    string     `json:"agent_version"`
	// PublicURL is the https origin this host's session runners are
	// reachable at from outside (§3.1, contract 1.2). The agent's edge
	// serves it; a runner builds its descriptor url from it. Empty means
	// not public, and the pool places no session work here.
	PublicURL    string       `json:"public_url,omitempty"`
	Hardware     []Hardware   `json:"hardware"`
	Capabilities []Capability `json:"capabilities"`
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
	// A compute unit was a GPU first (§3.1, contract 1.2). Kind "cpu"
	// makes this a socket: the id, model and memory fields keep their
	// names, and the CPU's own facts ride below.
	Kind    string   `json:"kind,omitempty"`
	Cores   int      `json:"cores,omitempty"`
	Threads int      `json:"threads,omitempty"`
	ISA     []string `json:"isa,omitempty"`
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
	// Draining tells the broker to stop dispatching here while the
	// container is still up, so in-flight work finishes (runner-attach
	// §3.2). Set from the pool's desired state; this is the field the
	// old profile expansion silently dropped.
	Draining bool `json:"draining,omitempty"`

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

// Runner is what the HOST knows about one container. Everything about
// what the container serves comes from its contract; this is where it
// is, which GPUs back it, and whether the pool is withdrawing it.
type Runner struct {
	LocalID  string   `json:"local_id"`
	URL      string   `json:"url"`
	Devices  []string `json:"devices,omitempty"`
	Draining bool     `json:"draining,omitempty"`
	// RTMPPort is the container port the agent's RTMPS edge forwards
	// to (plan 0046 §2.7); zero for a runner with no ingest. Agent-side
	// only: it never reaches the attach document.
	RTMPPort int `json:"-"`
}

// Host is the non-runner half of the document.
type Host struct {
	HostID       string
	AgentVersion string
	Credential   Credential
	PublicURL    string
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
// Build assembles the attach document from the host and the runners
// whose contracts were resolved. It never fetches: Resolve does, and
// keeping the two apart is what makes this testable without a server.
func Build(host Host, resolved []Resolved) (*Document, error) {
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
		ContractVersion: ContractVersionBase,
		Credential:      host.Credential,
		HostID:          host.HostID,
		AgentVersion:    host.AgentVersion,
		Hardware:        host.Hardware,
		Capabilities:    []Capability{},
	}
	for _, hw := range host.Hardware {
		if hw.Kind == "cpu" {
			doc.ContractVersion = ContractVersion
		}
	}
	if u := strings.TrimSpace(host.PublicURL); u != "" {
		if err := ValidatePublicURL(u); err != nil {
			return nil, err
		}
		doc.PublicURL = u
		doc.ContractVersion = ContractVersion
	}
	if doc.Hardware == nil {
		doc.Hardware = []Hardware{}
	}
	seen := map[string]bool{}
	for i, rs := range resolved {
		r := rs.Runner
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
		if rs.Contract == nil {
			return nil, fmt.Errorf("attach: runner %q has no contract; Resolve it first", r.LocalID)
		}
		doc.Capabilities = append(doc.Capabilities, capabilityOf(r, rs.Contract, doc.Hardware))
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

// ValidatePublicURL is the §3.1 shape: an https origin and nothing
// else. A path here would be silently folded into every runner's
// advertised url, and an http scheme would advertise a media endpoint
// with no TLS — both worth refusing before the document leaves the host.
func ValidatePublicURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("public_url: %w", err)
	}
	switch {
	case u.Scheme != "https":
		return fmt.Errorf("public_url %q: scheme must be https", raw)
	case u.Host == "":
		return fmt.Errorf("public_url %q: no host", raw)
	case u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil:
		return fmt.Errorf("public_url %q: an origin only — no path, query, fragment or userinfo", raw)
	case len(raw) > 256:
		return fmt.Errorf("public_url: longer than 256 characters")
	}
	return nil
}
