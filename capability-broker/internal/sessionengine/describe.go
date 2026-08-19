package sessionengine

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Runner self-description (paid-session/v1 §7.1.1).
//
// The runner is the only party that knows its own descriptor schema,
// work unit, API paths, and metering mode. Declaring those a second
// time in broker config creates two sources of truth that can disagree,
// and the broker's runtime cross-checks exist only because they do — a
// work-unit mismatch rejects every usage event for a session's
// lifetime. Reading the runner's own declaration turns that into a
// startup error.
//
// Advisory, never authoritative: published offerings are cold-key
// signed, so a runner's declaration is compared against config and
// reported, never adopted.

// RunnerDescription is what a runner reports about itself.
type RunnerDescription struct {
	Protocols    []string              `json:"protocols"`
	Capabilities []DescribedCapability `json:"capabilities"`
}

// DescribedCapability is one capability a runner implements.
type DescribedCapability struct {
	CapabilityID      string              `json:"capability_id"`
	DescriptorSchemas []string            `json:"descriptor_schemas"`
	WorkUnit          string              `json:"work_unit"`
	Metering          string              `json:"metering,omitempty"`
	Heartbeat         *DescribedHeartbeat `json:"heartbeat,omitempty"`
	Paths             map[string]string   `json:"paths,omitempty"`
	// Readiness is the runner's own readiness endpoint. The runner knows
	// what "ready" means for it (model loaded, GPU free, queue depth)
	// far better than an operator-authored probe recipe can approximate.
	Readiness *DescribedReadiness `json:"readiness,omitempty"`
}

// DescribedReadiness is a runner-declared readiness endpoint. Path is
// relative to the backend URL, like the other runner paths.
type DescribedReadiness struct {
	Path string `json:"path"`
}

// DescribedHeartbeat is the runner's own emit cadence.
type DescribedHeartbeat struct {
	IntervalSeconds int `json:"interval_seconds,omitempty"`
}

// Disagreement is one mismatch between a runner's declaration and the
// offering configured against it.
type Disagreement struct {
	Capability string
	Offering   string
	Field      string
	Configured string
	Declared   string
	// Fatal marks a contradiction that makes the capability unservable
	// (§7.1.1). Non-fatal entries are advisory warnings.
	Fatal bool
}

func (d Disagreement) String() string {
	kind := "warning"
	if d.Fatal {
		kind = "contradiction"
	}
	return fmt.Sprintf("%s %s/%s: %s configured %q but runner declares %q",
		kind, d.Capability, d.Offering, d.Field, d.Configured, d.Declared)
}

// CompareDescription checks a runner's declaration against the offering
// configured against it. It returns every disagreement rather than the
// first, so an operator fixing config sees the whole list.
//
// An empty or absent declaration yields nothing: a runner that does not
// self-describe is not in contradiction with anything.
func CompareDescription(spec *OfferingSpec, desc *RunnerDescription) []Disagreement {
	if spec == nil || desc == nil {
		return nil
	}
	var out []Disagreement
	add := func(field, configured, declared string, fatal bool) {
		out = append(out, Disagreement{
			Capability: spec.Capability, Offering: spec.Offering,
			Field: field, Configured: configured, Declared: declared, Fatal: fatal,
		})
	}

	if len(desc.Protocols) > 0 && !contains(desc.Protocols, "paid-session/v1") {
		add("protocol", "paid-session/v1", strings.Join(desc.Protocols, ","), true)
	}

	// Find the described capability matching this offering's capability
	// id. A runner serving several capabilities lists them all.
	var match *DescribedCapability
	for i := range desc.Capabilities {
		if desc.Capabilities[i].CapabilityID == spec.Capability {
			match = &desc.Capabilities[i]
			break
		}
	}
	if match == nil {
		if len(desc.Capabilities) == 0 {
			return out // nothing declared; nothing to compare
		}
		ids := make([]string, 0, len(desc.Capabilities))
		for _, c := range desc.Capabilities {
			ids = append(ids, c.CapabilityID)
		}
		add("capability_id", spec.Capability, strings.Join(ids, ","), true)
		return out
	}

	// Fatal: these make the configuration unservable as written.
	if len(match.DescriptorSchemas) > 0 && !contains(match.DescriptorSchemas, spec.DescriptorSchema) {
		add("descriptor_schema", spec.DescriptorSchema, strings.Join(match.DescriptorSchemas, ","), true)
	}
	if match.WorkUnit != "" && match.WorkUnit != spec.WorkUnit {
		// The expensive one: every usage event would be rejected.
		add("work_unit", spec.WorkUnit, match.WorkUnit, true)
	}

	// Advisory: real misconfigurations, but the broker's own settings win.
	if match.Metering != "" && spec.Metering != "" && match.Metering != spec.Metering {
		add("metering", spec.Metering, match.Metering, false)
	}
	if hb := match.Heartbeat; hb != nil && hb.IntervalSeconds > 0 {
		window := int(spec.heartbeat().Seconds()) * spec.missed()
		if hb.IntervalSeconds >= window {
			add("heartbeat", fmt.Sprintf("teardown after %ds", window),
				fmt.Sprintf("emits every %ds", hb.IntervalSeconds), false)
		}
	}
	for name, declared := range match.Paths {
		configured := ""
		switch name {
		case "create":
			configured = spec.RunnerPaths.Create
		case "status":
			configured = spec.RunnerPaths.Status
		case "terminate":
			configured = spec.RunnerPaths.Terminate
		default:
			continue
		}
		if configured != "" && declared != "" && configured != declared {
			add(name+"_path", configured, declared, false)
		}
	}
	return out
}

// FatalDisagreements filters to the contradictions.
func FatalDisagreements(ds []Disagreement) []Disagreement {
	var out []Disagreement
	for _, d := range ds {
		if d.Fatal {
			out = append(out, d)
		}
	}
	return out
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// Describe fetches a runner's self-description. A runner with no
// describe path, or one that cannot be reached, returns (nil, nil):
// unreachability is not a contradiction (§7.1.1).
func (c *HTTPRunnerClient) Describe(ctx context.Context, path string) (*RunnerDescription, error) {
	if path == "" {
		return nil, nil
	}
	var out RunnerDescription
	code, err := c.do(ctx, http.MethodGet, c.urlFor(path, ""), nil, &out)
	if err != nil {
		return nil, nil // unreachable: warn upstream, do not fail
	}
	if code/100 != 2 {
		return nil, fmt.Errorf("describe returned %d", code)
	}
	return &out, nil
}
