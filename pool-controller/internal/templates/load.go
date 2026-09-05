package templates

import (
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/gpu"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	templateIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	stepNameRE   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	promotedRE   = regexp.MustCompile(`^x-[A-Za-z0-9._-]+$`)
	priceWeiRE   = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	matchKeyRE   = regexp.MustCompile(`^identity\.[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)*$`)
)

// validStepTypes mirrors the broker's certification-steps contract. A
// pool that authors a step type the broker cannot run would have its
// offer rejected at push time, which is a slow and confusing way to
// learn about a typo.
var validStepTypes = map[string]bool{
	"readiness": true, "request": true, "usage": true, "latency": true,
}

// Catalog is the loaded set, indexed by id.
type Catalog struct {
	items map[string]Template
	order []string
}

// Load reads every *.yaml under dir as one template.
//
// A directory that does not exist is not an error: a pool that has not
// adopted a catalog yet runs with none, and the loader should not be
// the thing that stops it booting. A file that is present but wrong IS
// an error — a silently skipped template would leave a member running
// nothing with no explanation.
func Load(dir string) (*Catalog, error) {
	cat := &Catalog{items: map[string]Template{}}
	if strings.TrimSpace(dir) == "" {
		return cat, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return cat, nil
		}
		return nil, fmt.Errorf("read template catalog %s: %w", dir, err)
	}
	byOffering := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (!strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml")) {
			continue
		}
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var tmpl Template
		dec := yaml.NewDecoder(strings.NewReader(string(raw)))
		dec.KnownFields(true) // an unknown key is a typo, not an extension
		if err := dec.Decode(&tmpl); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if err := tmpl.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if prev, dup := cat.items[tmpl.ID]; dup {
			return nil, fmt.Errorf("%s: template id %q already declared by %q", path, tmpl.ID, prev.DisplayName)
		}
		// An offering id is unique across the WHOLE catalog, not per
		// capability: the broker admits one offer per id (offers.go,
		// "runners multiply an offer, entries do not") and rejects the
		// entire push otherwise. This check used to key on
		// (capability, offering_id), which let two templates through
		// that the broker then refused together — the e2e seam test
		// caught it on 2026-09-02 when a translations template reused
		// its transcription sibling's id.
		if prev, dup := byOffering[tmpl.OfferingID]; dup {
			return nil, fmt.Errorf("%s: template %q sells offering id %q, already sold by %q (%s) — one offer per id across the catalog",
				path, tmpl.ID, tmpl.OfferingID, prev, cat.items[prev].Capability)
		}
		byOffering[tmpl.OfferingID] = tmpl.ID
		cat.items[tmpl.ID] = tmpl
		cat.order = append(cat.order, tmpl.ID)
	}
	sort.Strings(cat.order)
	return cat, nil
}

// Get returns the template with this id.
func (c *Catalog) Get(id string) (Template, bool) {
	if c == nil {
		return Template{}, false
	}
	t, ok := c.items[id]
	return t, ok
}

// All returns every template, ordered by id so callers and their tests
// see a stable catalog.
func (c *Catalog) All() []Template {
	if c == nil {
		return nil
	}
	out := make([]Template, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.items[id])
	}
	return out
}

// Len reports how many templates loaded.
func (c *Catalog) Len() int {
	if c == nil {
		return 0
	}
	return len(c.items)
}

// Validate checks one template in isolation.
func (t Template) Validate() error {
	if !templateIDRE.MatchString(t.ID) {
		return fmt.Errorf("id %q must match %s", t.ID, templateIDRE)
	}
	if strings.TrimSpace(t.Capability) == "" {
		return fmt.Errorf("template %s: capability is required", t.ID)
	}
	if strings.TrimSpace(t.OfferingID) == "" {
		return fmt.Errorf("template %s: offering_id is required", t.ID)
	}
	switch t.Protocol {
	case "paid-job/v1", "paid-session/v1":
	default:
		return fmt.Errorf("template %s: protocol must be paid-job/v1 or paid-session/v1 (got %q)", t.ID, t.Protocol)
	}
	if !priceWeiRE.MatchString(t.PriceDefault.AmountWei) {
		return fmt.Errorf("template %s: price_default.amount_wei must be a non-negative decimal string (got %q)",
			t.ID, t.PriceDefault.AmountWei)
	}
	if t.Capacity.MaxInFlight < 0 || t.Capacity.QueueLimit < 0 {
		return fmt.Errorf("template %s: capacity values must be >= 0", t.ID)
	}
	for _, reserved := range []string{"protocol", "job", "session"} {
		if _, clash := t.Extra[reserved]; clash {
			return fmt.Errorf("template %s: extra.%s is reserved — the declaration owns that key", t.ID, reserved)
		}
	}
	for key := range t.Extra {
		if strings.HasPrefix(key, "x-") {
			return fmt.Errorf("template %s: extra.%s — x-* keys are runner extensions; promote them with extra_from_runner", t.ID, key)
		}
	}
	for _, reserved := range []string{"protocol", "job", "session"} {
		if _, clash := t.Constraints[reserved]; clash {
			return fmt.Errorf("template %s: constraints.%s is reserved", t.ID, reserved)
		}
	}
	seenPromoted := map[string]bool{}
	for _, key := range t.ExtraFromRunner {
		if !promotedRE.MatchString(key) {
			return fmt.Errorf("template %s: extra_from_runner %q must be an x-* key", t.ID, key)
		}
		if key == "x-certification-suggested" {
			return fmt.Errorf("template %s: extra_from_runner: x-certification-suggested is never promoted", t.ID)
		}
		if seenPromoted[key] {
			return fmt.Errorf("template %s: extra_from_runner repeats %q", t.ID, key)
		}
		seenPromoted[key] = true
	}
	// A session policy on a job offering would be silently ignored by
	// the broker, which is worse than refusing it: the operator would
	// believe they had set a lease.
	if t.SessionPolicy != nil && t.Protocol != "paid-session/v1" {
		return fmt.Errorf("template %s: session_policy is only valid for paid-session/v1", t.ID)
	}
	if p := t.SessionPolicy; p != nil {
		switch p.Attachment {
		case "", "external":
		default:
			return fmt.Errorf("template %s: session_policy.attachment must be external (got %q)", t.ID, p.Attachment)
		}
		switch p.Refill {
		case "", "extensible", "bounded":
		default:
			return fmt.Errorf("template %s: session_policy.refill must be extensible or bounded (got %q)", t.ID, p.Refill)
		}
		switch p.LeasePolicy {
		case "", "funding-tracking", "fixed":
		default:
			return fmt.Errorf("template %s: session_policy.lease_policy must be funding-tracking or fixed (got %q)", t.ID, p.LeasePolicy)
		}
		if p.LeaseMaxSeconds < 0 || p.MinRunwayUnits < 0 || p.MaxRotations < 0 || p.RunwayIncrementUnits < 0 {
			return fmt.Errorf("template %s: session_policy values must be >= 0", t.ID)
		}
		if p.Heartbeat.IntervalSeconds < 0 || p.Heartbeat.MissedThreshold < 0 {
			return fmt.Errorf("template %s: session_policy.heartbeat values must be >= 0", t.ID)
		}
	}
	for key, value := range t.Match {
		if !matchKeyRE.MatchString(key) {
			return fmt.Errorf("template %s: match key %q must be identity.<dotted key> (runner-attach §3.2)", t.ID, key)
		}
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("template %s: match.%s has no value", t.ID, key)
		}
	}
	seenStep := map[string]bool{}
	for i, step := range t.Certification {
		if !stepNameRE.MatchString(step.Name) {
			return fmt.Errorf("template %s: certification[%d].name %q must match %s", t.ID, i, step.Name, stepNameRE)
		}
		if seenStep[step.Name] {
			return fmt.Errorf("template %s: certification repeats step name %q", t.ID, step.Name)
		}
		seenStep[step.Name] = true
		if !validStepTypes[step.Type] {
			return fmt.Errorf("template %s: certification[%d] (%s): unsupported type %q", t.ID, i, step.Name, step.Type)
		}
		if step.TimeoutMS < 0 {
			return fmt.Errorf("template %s: certification[%d] (%s): timeout_ms must be >= 0", t.ID, i, step.Name)
		}
	}
	if t.Priority < 0 {
		return fmt.Errorf("template %s: priority must be >= 0", t.ID)
	}
	// A template that is neither a primary nor stackable anywhere can
	// never be placed, so it is a config error rather than a template
	// that simply never matches.
	if !t.Stacking.Primary && len(t.Stacking.SecondaryOn) == 0 {
		return fmt.Errorf("template %s: stacking declares neither primary nor secondary_on, so it can never be placed", t.ID)
	}
	if t.Probation.SharePPM > 1_000_000 || t.Active.ShareCapPPM > 1_000_000 {
		return fmt.Errorf("template %s: share values are parts per million and must be <= 1000000", t.ID)
	}
	if t.CommissionBPS > 10_000 {
		return fmt.Errorf("template %s: commission_bps must be <= 10000", t.ID)
	}
	// The image map and the class list have to agree, or a card is placed
	// on a workload it has no image for and the failure surfaces as a
	// compose pull on a member's host instead of here (plan 0045 §4).
	for key, image := range t.RunnerCompose.Image {
		vendor, class := SplitImageKey(key)
		if !gpu.Known(vendor) {
			return fmt.Errorf("template %s: runner_compose.image names vendor %q; known vendors are %v",
				t.ID, vendor, gpu.Vendors())
		}
		// A class key is a build for one class; the class has to be one
		// the pool knows, of the vendor the key names, or the override
		// would sit there matching nothing.
		if class != "" && gpu.VendorOfClass(class) != vendor {
			return fmt.Errorf("template %s: runner_compose.image key %q: %q is not a %s class the pool knows (%v)",
				t.ID, key, class, vendor, gpu.Classes())
		}
		if strings.TrimSpace(image) == "" {
			return fmt.Errorf("template %s: runner_compose.image.%s is empty", t.ID, key)
		}
	}
	if t.RunnerCompose.HasImage() && len(t.Requirements.CPUClasses) > 0 && t.RunnerCompose.ImageFor(gpu.VendorCPU) == "" {
		return fmt.Errorf("template %s: requirements.cpu_classes admits sockets but runner_compose.image has no cpu image", t.ID)
	}
	for _, class := range t.Requirements.CPUClasses {
		if gpu.VendorOfClass(class) != gpu.VendorCPU {
			return fmt.Errorf("template %s: requirements.cpu_classes names %q; cpu classes are core tiers cpu-8, cpu-16, cpu-32, cpu-64", t.ID, class)
		}
	}
	if t.RunnerCompose.RTMPPort < 0 || t.RunnerCompose.RTMPPort > 65535 {
		return fmt.Errorf("template %s: runner_compose.rtmp_port must be a port", t.ID)
	}
	if t.RunnerCompose.RTMPPort > 0 && t.Protocol != "paid-session/v1" {
		return fmt.Errorf("template %s: runner_compose.rtmp_port is only meaningful on paid-session/v1 (an ingest is a session)", t.ID)
	}
	if t.RunnerCompose.HasImage() {
		for _, class := range t.Requirements.GPUClasses {
			vendor := gpu.VendorOfClass(class)
			if vendor == "" {
				continue // a class placement has not learned yet is not this check's business
			}
			if t.RunnerCompose.ImageForClass(vendor, class) == "" {
				return fmt.Errorf("template %s: requirements.gpu_classes admits %s (%s) but runner_compose.image has no %s image for it; "+
					"a card placed on it would fail at compose up on a member's host", t.ID, class, vendor, vendor)
			}
		}
	}
	for i, model := range t.RunnerCompose.Models {
		if strings.TrimSpace(model.Name) == "" {
			return fmt.Errorf("template %s: runner_compose.models[%d].name is required", t.ID, i)
		}
	}
	return nil
}
