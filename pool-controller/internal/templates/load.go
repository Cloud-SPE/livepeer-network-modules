package templates

import (
	"fmt"
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
		// Two templates selling the same (capability, offering) would
		// race to define one offer; the broker admits exactly one.
		key := tmpl.Capability + "|" + tmpl.OfferingID
		if prev, dup := byOffering[key]; dup {
			return nil, fmt.Errorf("%s: template %q sells %s/%s, already sold by %q",
				path, tmpl.ID, tmpl.Capability, tmpl.OfferingID, prev)
		}
		byOffering[key] = tmpl.ID
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
	for i, model := range t.RunnerCompose.Models {
		if strings.TrimSpace(model.Name) == "" {
			return fmt.Errorf("template %s: runner_compose.models[%d].name is required", t.ID, i)
		}
	}
	return nil
}
