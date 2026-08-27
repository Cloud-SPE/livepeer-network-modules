// Package runnerattach evaluates runner attach documents
// (livepeer-network-protocol/protocols/runner-attach.md).
//
// The document is the only way runner facts enter the broker. This
// package owns §3 (shape) and §4 (validation order and rejection codes)
// and produces the §6 register_result. It does not touch offers, freeze,
// or eligibility (§5) — those are the broker's runner registry and offer
// engine — but it does compute the §5 frozen projection of an accepted
// capability, since that is a pure function of the document.
//
// Order is fixed so a sender can rely on which class of failure it hears
// first: parse → size → contract_version → credential → shape → host
// rules → per-capability rules.
package runnerattach

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ContractMajor is the contract major this broker implements.
const ContractMajor = 1

// MaxDocumentBytes is the §3 size bound.
const MaxDocumentBytes = 256 * 1024

const maxExtensionBytes = 32 * 1024

// Known is what the broker implements; the validator never guesses.
type Known struct {
	// Extractor reports whether a work_unit.extractor.type is registered.
	Extractor func(name string) bool
	// ProbeTypes are the remote readiness probe types (§3.2). Broker-local
	// kinds (command-exit-0, manual-drain) are excluded by the caller.
	ProbeTypes map[string]bool
	// DescriptorSchemas the broker will carry (protocol module descriptors/).
	DescriptorSchemas map[string]bool
	// Protocols are the paid protocols served (paid-job/v1, paid-session/v1).
	Protocols map[string]bool
	// Credential resolves a bearer token to an enrollment; nil host id
	// means rejected. HostID from the enrollment, when recorded, must
	// match the document's.
	Credential func(kind, token string) (enrolledHostID string, ok bool, kindSupported bool)
}

// Document is an accepted attach document.
type Document struct {
	ContractVersion string                     `json:"contract_version"`
	Credential      Credential                 `json:"credential"`
	HostID          string                     `json:"host_id"`
	AgentVersion    string                     `json:"agent_version"`
	Hardware        []Hardware                 `json:"hardware"`
	Capabilities    []Capability               `json:"capabilities"`
	Extensions      map[string]json.RawMessage `json:"-"`
}

// Credential is the §3.1.1 object. Token is never stored past
// evaluation; it is zeroed once the credential store has answered.
type Credential struct {
	Kind      string `json:"kind"`
	Token     string `json:"token,omitempty"`
	KeyID     string `json:"key_id,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// Hardware is one GPU.
type Hardware struct {
	GPUUUID   string            `json:"gpu_uuid"`
	GPUModel  string            `json:"gpu_model"`
	VRAMBytes uint64            `json:"vram_bytes"`
	Driver    string            `json:"driver,omitempty"`
	CUDA      string            `json:"cuda,omitempty"`
	Facts     map[string]string `json:"facts,omitempty"`
}

// Capability is one capability entry as accepted (§3.2).
type Capability struct {
	Index               int               `json:"-"`
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
	// Draining says this runner is winding down and should be sent no
	// new work. It is deliberately NOT part of the frozen shape: what a
	// runner sells has not changed, only whether it is currently taking
	// orders, and freezing it would make a temporary withdrawal look
	// like a different offering.
	Draining   bool                       `json:"draining,omitempty"`
	Extensions map[string]json.RawMessage `json:"-"` // x-* incl. x-certification-suggested
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

// IsJob / IsSession by protocol prefix.
func (c *Capability) IsJob() bool     { return strings.HasPrefix(c.Protocol, "paid-job/") }
func (c *Capability) IsSession() bool { return strings.HasPrefix(c.Protocol, "paid-session/") }

// Reason is one §6 reason.
type Reason struct {
	Code     string `json:"code"`
	Field    string `json:"field,omitempty"`
	Declared string `json:"declared,omitempty"`
	Expected string `json:"expected,omitempty"`
	Message  string `json:"message,omitempty"`
}

// CapabilityResult is one entry of the §6 capabilities[] list.
type CapabilityResult struct {
	Index        int      `json:"index"`
	LocalID      string   `json:"local_id"`
	CapabilityID string   `json:"capability_id"`
	Status       string   `json:"status"` // accepted | rejected
	Reasons      []Reason `json:"reasons,omitempty"`
	Warnings     []Reason `json:"warnings,omitempty"`
}

// Result is the §6 register_result body.
type Result struct {
	ContractVersion string             `json:"contract_version"`
	Document        string             `json:"document"` // accepted | rejected
	HostID          string             `json:"host_id,omitempty"`
	Reasons         []Reason           `json:"reasons"`
	Capabilities    []CapabilityResult `json:"capabilities"`
}

// Rejected is a convenience for the document-level case.
func (r *Result) Rejected() bool { return r.Document == "rejected" }

var (
	hostIDRE      = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	localIDRE     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	tagRE         = regexp.MustCompile(`^[a-z][a-z0-9-]*/v[0-9]+$`)
	semverRE      = regexp.MustCompile(`^[0-9]+\.[0-9]+(\.[0-9]+)?(-[0-9A-Za-z.-]+)?$`)
	identityKeyRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)*$`)
	extractorRE   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	contractRE    = regexp.MustCompile(`^([0-9]+)\.([0-9]+)$`)
)

var transports = map[string]bool{"unary": true, "stream": true, "multipart": true}

// Field sets for unknown-field detection (§4.1). Anything not here and
// not x-* is unknown; contents of opaque blobs are not walked.
var (
	hostFields = set("contract_version", "credential", "host_id", "agent_version", "hardware", "capabilities")
	credFields = set("kind", "token", "key_id", "signature")
	hwFields   = set("gpu_uuid", "gpu_model", "vram_bytes", "driver", "cuda", "facts")
	capFields  = set("capability_id", "protocol", "local_id", "transports", "descriptor_schemas", "work_unit",
		"paths", "readiness", "identity", "schema_versions", "metering", "heartbeat", "session_params_schema",
		"requirements", "devices")
	wuFields   = set("name", "extractor")
	pathFields = set("invoke", "options", "create", "status", "terminate")
	rdFields   = set("type", "path", "config")
	hbFields   = set("interval_seconds")
	reqFields  = set("gpu_vram_min_bytes", "gpu_models")
)

func set(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// Evaluate runs the full §4 pipeline. The Document is non-nil only when
// the result is accepted; rejected capabilities are absent from
// Document.Capabilities but present in Result.Capabilities.
func Evaluate(raw []byte, known Known) (*Document, *Result) {
	res := &Result{ContractVersion: fmt.Sprintf("%d.0", ContractMajor), Reasons: []Reason{}, Capabilities: []CapabilityResult{}}
	reject := func(code, field, msg string) (*Document, *Result) {
		res.Document = "rejected"
		res.Reasons = append(res.Reasons, Reason{Code: code, Field: field, Message: msg})
		return nil, res
	}

	// parse + size
	if len(raw) > MaxDocumentBytes {
		return reject("malformed", "", fmt.Sprintf("document exceeds %d bytes", MaxDocumentBytes))
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		return reject("malformed", "", "document must be a single JSON object")
	}

	// contract_version
	var cv string
	if err := json.Unmarshal(top["contract_version"], &cv); err != nil || !contractRE.MatchString(cv) {
		return reject("contract_version_unsupported", "/contract_version", "want <major>.<minor>")
	}
	if m := contractRE.FindStringSubmatch(cv); m != nil {
		if major, _ := strconv.Atoi(m[1]); major != ContractMajor {
			res.Reasons = append(res.Reasons, Reason{Code: "contract_version_unsupported", Field: "/contract_version",
				Declared: cv, Expected: fmt.Sprintf("major %d", ContractMajor)})
			res.Document = "rejected"
			return nil, res
		}
	}

	// credential (before shape, so an impostor learns nothing about shape rules)
	var cred Credential
	credRaw, hasCred := top["credential"]
	if !hasCred {
		return reject("schema_violation", "/credential", "required")
	}
	if code, field := checkUnknown(credRaw, credFields, "/credential", false); code != "" {
		return reject(code, field, "")
	}
	if err := json.Unmarshal(credRaw, &cred); err != nil || cred.Kind == "" {
		return reject("schema_violation", "/credential", "kind is required")
	}
	if known.Credential == nil {
		return reject("credential_rejected", "/credential", "")
	}
	enrolledHost, ok, kindSupported := known.Credential(cred.Kind, cred.Token)
	if !kindSupported {
		return reject("credential_kind_unsupported", "/credential/kind", "")
	}
	if !ok {
		return reject("credential_rejected", "/credential", "")
	}
	cred.Token, cred.Signature = "", ""

	// shape: unknown fields anywhere in the defined structure
	if code, field := checkUnknown(raw, hostFields, "", true); code != "" {
		return reject(code, field, "")
	}
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return reject("schema_violation", "", err.Error())
	}
	doc.Credential = cred
	doc.Extensions = extensions(top)
	if !hostIDRE.MatchString(doc.HostID) {
		return reject("schema_violation", "/host_id", "must match [A-Za-z0-9._-]{1,128}")
	}
	if enrolledHost != "" && enrolledHost != doc.HostID {
		res.Document = "rejected"
		res.Reasons = append(res.Reasons, Reason{Code: "host_id_mismatch", Field: "/host_id", Declared: doc.HostID, Expected: enrolledHost})
		return nil, res
	}
	res.HostID = doc.HostID
	if doc.AgentVersion == "" || len(doc.AgentVersion) > 128 {
		return reject("schema_violation", "/agent_version", "required, ≤ 128 chars")
	}
	if _, ok := top["hardware"]; !ok {
		return reject("schema_violation", "/hardware", "required (may be empty)")
	}
	if _, ok := top["capabilities"]; !ok {
		return reject("schema_violation", "/capabilities", "required (may be empty)")
	}
	if len(doc.Hardware) > 64 || len(doc.Capabilities) > 64 {
		return reject("schema_violation", "", "at most 64 hardware units and 64 capabilities")
	}
	if n := extensionBytes(doc.Extensions); n > maxExtensionBytes {
		return reject("schema_violation", "", fmt.Sprintf("host-level x-* payload %d > %d bytes", n, maxExtensionBytes))
	}
	// hardware: shape + duplicate uuid
	var hwRaw []json.RawMessage
	_ = json.Unmarshal(top["hardware"], &hwRaw)
	seenGPU := map[string]int{}
	for i, h := range doc.Hardware {
		field := fmt.Sprintf("/hardware/%d", i)
		if i < len(hwRaw) {
			if code, f := checkUnknown(hwRaw[i], hwFields, field, false); code != "" {
				return reject(code, f, "")
			}
		}
		if h.GPUUUID == "" || len(h.GPUUUID) > 128 || h.GPUModel == "" || len(h.GPUModel) > 128 {
			return reject("schema_violation", field, "gpu_uuid and gpu_model required, ≤ 128 chars")
		}
		if len(h.Facts) > 32 {
			return reject("schema_violation", field+"/facts", "at most 32 keys")
		}
		if prev, dup := seenGPU[h.GPUUUID]; dup {
			res.Document = "rejected"
			res.Reasons = append(res.Reasons, Reason{Code: "duplicate_gpu_uuid", Field: field + "/gpu_uuid",
				Declared: h.GPUUUID, Message: fmt.Sprintf("also at /hardware/%d", prev)})
			return nil, res
		}
		seenGPU[h.GPUUUID] = i
	}

	// capabilities: unknown fields are document-level; values are entry-level
	var capRaw []json.RawMessage
	_ = json.Unmarshal(top["capabilities"], &capRaw)
	for i := range capRaw {
		field := fmt.Sprintf("/capabilities/%d", i)
		if code, f := checkUnknown(capRaw[i], capFields, field, true); code != "" {
			return reject(code, f, "")
		}
		var sub map[string]json.RawMessage
		_ = json.Unmarshal(capRaw[i], &sub)
		for name, fields := range map[string]map[string]bool{"work_unit": wuFields, "paths": pathFields, "readiness": rdFields, "heartbeat": hbFields, "requirements": reqFields} {
			if r, ok := sub[name]; ok {
				if code, f := checkUnknown(r, fields, field+"/"+name, false); code != "" {
					return reject(code, f, "")
				}
			}
		}
	}
	seenCap := map[string]int{}
	seenLocal := map[string]int{}
	accepted := make([]Capability, 0, len(doc.Capabilities))
	for i := range doc.Capabilities {
		c := &doc.Capabilities[i]
		c.Index = i
		if i < len(capRaw) {
			var sub map[string]json.RawMessage
			_ = json.Unmarshal(capRaw[i], &sub)
			c.Extensions = extensions(sub)
		}
		if c.LocalID == "" {
			c.LocalID = strconv.Itoa(i)
		}
		field := fmt.Sprintf("/capabilities/%d", i)
		// document-level duplicates
		if prev, dup := seenLocal[c.LocalID]; dup {
			res.Document = "rejected"
			res.Capabilities = []CapabilityResult{}
			res.Reasons = append(res.Reasons, Reason{Code: "duplicate_capability", Field: field + "/local_id",
				Declared: c.LocalID, Message: fmt.Sprintf("also at /capabilities/%d", prev)})
			return nil, res
		}
		seenLocal[c.LocalID] = i
		key := c.CapabilityID + "|" + canonicalIdentity(c.Identity)
		if prev, dup := seenCap[key]; dup {
			res.Document = "rejected"
			res.Capabilities = []CapabilityResult{}
			res.Reasons = append(res.Reasons, Reason{Code: "duplicate_capability", Field: field,
				Declared: c.CapabilityID, Message: fmt.Sprintf("same capability_id and identity as /capabilities/%d", prev)})
			return nil, res
		}
		seenCap[key] = i

		cr := CapabilityResult{Index: i, LocalID: c.LocalID, CapabilityID: c.CapabilityID, Status: "accepted"}
		reasons := validateCapability(c, &doc, known, field)
		if len(reasons) > 0 {
			cr.Status = "rejected"
			cr.Reasons = reasons
		} else {
			accepted = append(accepted, *c)
		}
		res.Capabilities = append(res.Capabilities, cr)
	}
	doc.Capabilities = accepted
	res.Document = "accepted"
	return &doc, res
}

// validateCapability applies §4.2 to one entry; every failure is
// collected so an operator sees the whole list.
func validateCapability(c *Capability, doc *Document, known Known, field string) []Reason {
	var out []Reason
	add := func(code, sub, declared, expected string) {
		out = append(out, Reason{Code: code, Field: field + sub, Declared: declared, Expected: expected})
	}
	if c.CapabilityID == "" || len(c.CapabilityID) > 256 {
		add("schema_violation", "/capability_id", c.CapabilityID, "1–256 chars")
	}
	if !localIDRE.MatchString(c.LocalID) {
		add("schema_violation", "/local_id", c.LocalID, "[A-Za-z0-9._-]{1,64}")
	}
	if !tagRE.MatchString(c.Protocol) {
		add("schema_violation", "/protocol", c.Protocol, "<name>/v<N>")
		return out
	}
	if !known.Protocols[c.Protocol] {
		add("protocol_unknown", "/protocol", c.Protocol, keys(known.Protocols))
		return out
	}
	isJob, isSession := c.IsJob(), c.IsSession()

	// work unit
	if c.WorkUnit.Name == "" || len(c.WorkUnit.Name) > 64 {
		add("schema_violation", "/work_unit/name", c.WorkUnit.Name, "1–64 chars")
	}
	if isJob {
		if len(c.Transports) == 0 {
			add("schema_violation", "/transports", "", "non-empty subset of unary|stream|multipart")
		}
		seenT := map[string]bool{}
		for _, t := range c.Transports {
			if !transports[t] || seenT[t] {
				add("schema_violation", "/transports", t, "unique values from unary|stream|multipart")
			}
			seenT[t] = true
		}
		if len(c.DescriptorSchemas) > 0 || c.Metering != "" || c.Heartbeat != nil || len(c.SessionParamsSchema) > 0 {
			add("schema_violation", "", "session fields", "none on a paid-job capability")
		}
		if c.WorkUnit.Extractor == nil {
			add("schema_violation", "/work_unit/extractor", "", "required for paid-job")
		} else {
			typ, _ := c.WorkUnit.Extractor["type"].(string)
			switch {
			case !extractorRE.MatchString(typ):
				add("schema_violation", "/work_unit/extractor/type", typ, "[a-z][a-z0-9-]*")
			case known.Extractor == nil || !known.Extractor(typ):
				add("extractor_unknown", "/work_unit/extractor/type", typ, "a registered extractor")
			}
		}
		if _, ok := c.Paths["invoke"]; !ok {
			add("schema_violation", "/paths/invoke", "", "required for paid-job")
		}
		for _, k := range []string{"create", "status", "terminate"} {
			if _, ok := c.Paths[k]; ok {
				add("schema_violation", "/paths/"+k, c.Paths[k], "not valid on a paid-job capability")
			}
		}
	}
	if isSession {
		if len(c.Transports) > 0 {
			add("schema_violation", "/transports", strings.Join(c.Transports, ","), "not valid on a paid-session capability")
		}
		if len(c.DescriptorSchemas) == 0 {
			add("schema_violation", "/descriptor_schemas", "", "non-empty list of <name>/v<N>")
		}
		seenD := map[string]bool{}
		for _, d := range c.DescriptorSchemas {
			switch {
			case !tagRE.MatchString(d) || seenD[d]:
				add("schema_violation", "/descriptor_schemas", d, "unique <name>/v<N> tags")
			case !known.DescriptorSchemas[d]:
				add("descriptor_schema_unknown", "/descriptor_schemas", d, keys(known.DescriptorSchemas))
			}
			seenD[d] = true
		}
		switch c.Metering {
		case "runner-reported", "broker-observed":
		case "":
			add("schema_violation", "/metering", "", "runner-reported | broker-observed (required for paid-session)")
		default:
			add("schema_violation", "/metering", c.Metering, "runner-reported | broker-observed")
		}
		if c.WorkUnit.Extractor != nil {
			add("schema_violation", "/work_unit/extractor", "", "absent on paid-session — usage is runner-reported")
		}
		if c.Heartbeat != nil && c.Heartbeat.IntervalSeconds < 1 {
			add("schema_violation", "/heartbeat/interval_seconds", strconv.Itoa(c.Heartbeat.IntervalSeconds), ">= 1")
		}
		if len(c.SessionParamsSchema) > 16*1024 {
			add("schema_violation", "/session_params_schema", "", "≤ 16 KiB")
		}
		for _, k := range []string{"create", "status", "terminate"} {
			if _, ok := c.Paths[k]; !ok {
				add("schema_violation", "/paths/"+k, "", "required for paid-session")
			}
		}
		for _, k := range []string{"status", "terminate"} {
			if p, ok := c.Paths[k]; ok && !strings.Contains(p, "{id}") {
				add("path_invalid", "/paths/"+k, p, "must contain {id}")
			}
		}
		for _, k := range []string{"invoke", "options"} {
			if _, ok := c.Paths[k]; ok {
				add("schema_violation", "/paths/"+k, c.Paths[k], "not valid on a paid-session capability")
			}
		}
	}
	// paths grammar
	if c.Paths == nil {
		add("schema_violation", "/paths", "", "required")
	}
	for k, p := range c.Paths {
		if !pathOK(p) {
			add("path_invalid", "/paths/"+k, p, "relative, starts with /, no .., ?, #, or scheme")
		}
	}
	// readiness
	switch {
	case c.Readiness.Type == "":
		add("schema_violation", "/readiness/type", "", "required")
	case !known.ProbeTypes[c.Readiness.Type]:
		add("readiness_type_unknown", "/readiness/type", c.Readiness.Type, keys(known.ProbeTypes))
	}
	if c.Readiness.Path != "" && !pathOK(c.Readiness.Path) {
		add("path_invalid", "/readiness/path", c.Readiness.Path, "relative path")
	}
	// identity
	if c.Identity == nil {
		add("schema_violation", "/identity", "", "required (may be {})")
	}
	if len(c.Identity) > 32 {
		add("identity_invalid", "/identity", "", "≤ 32 keys")
	}
	idKeys := make([]string, 0, len(c.Identity))
	for k, v := range c.Identity {
		if !identityKeyRE.MatchString(k) {
			add("identity_invalid", "/identity/"+k, k, "dotted lowercase key")
		}
		if len(v) > 256 {
			add("identity_invalid", "/identity/"+k, "", "value ≤ 256 chars")
		}
		idKeys = append(idKeys, k)
	}
	sort.Strings(idKeys)
	for i := 0; i+1 < len(idKeys); i++ {
		if strings.HasPrefix(idKeys[i+1], idKeys[i]+".") {
			add("identity_invalid", "/identity/"+idKeys[i], idKeys[i], "a key must not be both a leaf and a prefix ("+idKeys[i+1]+")")
		}
	}
	// schema_versions
	if len(c.SchemaVersions) == 0 {
		add("schema_version_missing", "/schema_versions", "", "entry for "+c.Protocol+" and each descriptor schema")
	} else {
		need := append([]string{c.Protocol}, c.DescriptorSchemas...)
		for _, tag := range need {
			v, ok := c.SchemaVersions[tag]
			if !ok {
				add("schema_version_missing", "/schema_versions/"+tag, "", "required")
				continue
			}
			if !semverRE.MatchString(v) {
				add("schema_violation", "/schema_versions/"+tag, v, "SemVer")
				continue
			}
			if tagMajor(tag) != semverMajor(v) {
				add("schema_version_major_mismatch", "/schema_versions/"+tag, v, "major "+strconv.Itoa(tagMajor(tag)))
			}
		}
		for tag := range c.SchemaVersions {
			if !tagRE.MatchString(tag) {
				add("schema_violation", "/schema_versions/"+tag, tag, "<name>/v<N> key")
			}
		}
	}
	// requirements vs this host's hardware
	if r := c.Requirements; r != nil {
		if r.GPUVRAMMinBytes > 0 || len(r.GPUModels) > 0 {
			if !hardwareSatisfies(doc.Hardware, r) {
				add("requirements_unmet", "/requirements", describeReq(r), describeHW(doc.Hardware))
			}
		}
	}
	// devices
	seenDev := map[string]bool{}
	for _, d := range c.Devices {
		if seenDev[d] {
			add("schema_violation", "/devices", d, "unique gpu_uuid values")
		}
		seenDev[d] = true
		found := false
		for _, h := range doc.Hardware {
			if h.GPUUUID == d {
				found = true
				break
			}
		}
		if !found {
			add("device_unknown", "/devices", d, "a gpu_uuid from hardware[]")
		}
	}
	// extensions bound
	if n := extensionBytes(c.Extensions); n > maxExtensionBytes {
		add("schema_violation", "", strconv.Itoa(n), fmt.Sprintf("x-* payload ≤ %d bytes", maxExtensionBytes))
	}
	return out
}

// --- helpers -------------------------------------------------------------

// checkUnknown walks one object's keys against allowed. With allowX,
// x-* keys pass. Returns ("unknown_field", pointer) on the first
// unknown key, ("schema_violation", pointer) when raw is not an object.
func checkUnknown(raw json.RawMessage, allowed map[string]bool, pointer string, allowX bool) (string, string) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		if pointer == "" {
			pointer = "/"
		}
		return "schema_violation", pointer
	}
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if allowed[k] || (allowX && strings.HasPrefix(k, "x-")) {
			continue
		}
		return "unknown_field", pointer + "/" + k
	}
	return "", ""
}

func extensions(m map[string]json.RawMessage) map[string]json.RawMessage {
	var out map[string]json.RawMessage
	for k, v := range m {
		if strings.HasPrefix(k, "x-") {
			if out == nil {
				out = map[string]json.RawMessage{}
			}
			out[k] = v
		}
	}
	return out
}

func extensionBytes(m map[string]json.RawMessage) int {
	n := 0
	for k, v := range m {
		n += len(k) + len(v)
	}
	return n
}

func pathOK(p string) bool {
	return strings.HasPrefix(p, "/") && len(p) <= 512 && !strings.Contains(p, "..") &&
		!strings.ContainsAny(p, "?#") && !strings.Contains(p, "://")
}

func tagMajor(tag string) int {
	i := strings.LastIndex(tag, "/v")
	if i < 0 {
		return -1
	}
	n, err := strconv.Atoi(tag[i+2:])
	if err != nil {
		return -1
	}
	return n
}

func semverMajor(v string) int {
	n, err := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	if err != nil {
		return -2
	}
	return n
}

func hardwareSatisfies(hw []Hardware, r *Requirements) bool {
	for _, h := range hw {
		if r.GPUVRAMMinBytes > 0 && h.VRAMBytes < r.GPUVRAMMinBytes {
			continue
		}
		if len(r.GPUModels) > 0 {
			ok := false
			for _, m := range r.GPUModels {
				if strings.TrimSpace(m) == strings.TrimSpace(h.GPUModel) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		return true
	}
	return false
}

func describeReq(r *Requirements) string {
	parts := []string{}
	if r.GPUVRAMMinBytes > 0 {
		parts = append(parts, fmt.Sprintf("vram>=%d", r.GPUVRAMMinBytes))
	}
	if len(r.GPUModels) > 0 {
		parts = append(parts, "models="+strings.Join(r.GPUModels, "|"))
	}
	return strings.Join(parts, " ")
}

func describeHW(hw []Hardware) string {
	if len(hw) == 0 {
		return "no hardware declared"
	}
	parts := make([]string, 0, len(hw))
	for _, h := range hw {
		parts = append(parts, fmt.Sprintf("%s(%d)", h.GPUModel, h.VRAMBytes))
	}
	return strings.Join(parts, ", ")
}

func keys(m map[string]bool) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return "one of: " + strings.Join(out, ", ")
}

func canonicalIdentity(id map[string]string) string {
	b, _ := json.Marshal(id) // map keys are sorted by encoding/json
	return string(b)
}

// --- §5 frozen projection -------------------------------------------------

// Projection is the §5 frozen shape of an accepted capability.
type Projection struct {
	Protocol          string                     `json:"protocol"`
	Transports        []string                   `json:"transports,omitempty"`
	DescriptorSchemas []string                   `json:"descriptor_schemas,omitempty"`
	WorkUnit          WorkUnit                   `json:"work_unit"`
	Metering          string                     `json:"metering,omitempty"`
	Identity          map[string]string          `json:"identity"`
	SchemaVersions    map[string]string          `json:"schema_versions"` // majors only
	Promoted          map[string]json.RawMessage `json:"promoted,omitempty"`
}

// Project computes the projection with the given promoted x-* keys.
func Project(c *Capability, promoted []string) Projection {
	p := Projection{
		Protocol: c.Protocol, WorkUnit: c.WorkUnit, Metering: c.Metering,
		Identity: c.Identity, SchemaVersions: map[string]string{},
	}
	if c.IsJob() {
		p.Transports = append([]string(nil), c.Transports...)
		sort.Strings(p.Transports)
	} else {
		p.DescriptorSchemas = append([]string(nil), c.DescriptorSchemas...)
		sort.Strings(p.DescriptorSchemas)
	}
	if p.Identity == nil {
		p.Identity = map[string]string{}
	}
	for tag, v := range c.SchemaVersions {
		p.SchemaVersions[tag] = strconv.Itoa(semverMajor(v))
	}
	for _, k := range promoted {
		if v, ok := c.Extensions[k]; ok {
			if p.Promoted == nil {
				p.Promoted = map[string]json.RawMessage{}
			}
			p.Promoted[k] = v
		}
	}
	return p
}

// Canonical returns the JCS-style canonical bytes of a projection
// (sorted keys, no whitespace — encoding/json already does both for
// maps and structs) and its sha256 hash as "sha256:<hex>".
func (p Projection) Canonical() ([]byte, string, error) {
	// Round-trip through a generic value so RawMessage promoted values
	// are re-encoded compactly with sorted keys.
	b, err := json.Marshal(p)
	if err != nil {
		return nil, "", err
	}
	var generic any
	if err := json.Unmarshal(b, &generic); err != nil {
		return nil, "", err
	}
	canon, err := json.Marshal(generic)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(canon)
	return canon, "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Diff lists the fields where two projections disagree (§5 "naming the
// disagreeing field").
func Diff(frozen, candidate Projection) []Reason {
	var fa, fb map[string]any
	ba, _, _ := frozen.Canonical()
	bb, _, _ := candidate.Canonical()
	_ = json.Unmarshal(ba, &fa)
	_ = json.Unmarshal(bb, &fb)
	var out []Reason
	diffInto(&out, "", fa, fb)
	return out
}

func diffInto(out *[]Reason, prefix string, a, b any) {
	ma, aok := a.(map[string]any)
	mb, bok := b.(map[string]any)
	if aok && bok {
		names := map[string]bool{}
		for k := range ma {
			names[k] = true
		}
		for k := range mb {
			names[k] = true
		}
		sorted := make([]string, 0, len(names))
		for k := range names {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			diffInto(out, prefix+"/"+k, ma[k], mb[k])
		}
		return
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		*out = append(*out, Reason{Code: "shape_mismatch", Field: prefix, Declared: string(jb), Expected: string(ja)})
	}
}

// ErrNoStore is returned by a Credential func adapter when the broker has
// no credential store configured.
var ErrNoStore = errors.New("runnerattach: no credential store")
