// Package templates is the pool's curated workload catalog.
//
// A template is one sellable workload: what it is, what hardware may
// run it, how it is certified, and what it costs by default. It lives
// in a YAML file under the repo's templates/ directory, not in the
// controller's database, because the catalog is a decision the pool
// makes once and reviews in version control — an operator should be
// able to read the diff that changed what their members run.
//
// What a pool varies per deployment is small and lives in the database
// instead: whether a template is enabled, what it actually charges, and
// any extra metadata to advertise (plan 0044 §3.2). That split is the
// point. A price is a local commercial decision; a certification recipe
// or a GPU requirement is not.
package templates

import "github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"

// Template is one entry of the curated catalog.
type Template struct {
	ID          string `yaml:"id" json:"id"`
	Capability  string `yaml:"capability" json:"capability"`
	OfferingID  string `yaml:"offering_id" json:"offering_id"`
	Protocol    string `yaml:"protocol" json:"protocol"`
	DisplayName string `yaml:"display_name,omitempty" json:"display_name,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// PriceDefault is a starting point, not a rate card. A pool that
	// enables this template without setting its own price is saying it
	// accepts the catalog's suggestion.
	PriceDefault Price    `yaml:"price_default" json:"price_default"`
	Capacity     Capacity `yaml:"capacity,omitempty" json:"capacity,omitempty"`

	// Extra is operator metadata advertised with the offer. Runner-owned
	// facts never appear here — the broker freezes those from the attach
	// document. ExtraFromRunner names the x-* keys a runner may promote.
	Extra           map[string]any `yaml:"extra,omitempty" json:"extra,omitempty"`
	ExtraFromRunner []string       `yaml:"extra_from_runner,omitempty" json:"extra_from_runner,omitempty"`

	// Certification is the pool's proof that a matched runner can serve
	// this workload. The pool authors it and the broker executes it; a
	// runner may suggest steps and never self-certify.
	Certification []types.CertificationStep `yaml:"certification,omitempty" json:"certification,omitempty"`

	// Match selects which attached runners this template wants, by the
	// identity they declared (runner-attach §3.2). Two templates of the
	// same capability — a 20B and a 70B chat model, say — are told
	// apart only by this: without it both would match every chat runner
	// in the pool and the broker would freeze whichever arrived first.
	// Left empty, the offer takes any runner serving the capability,
	// which is what a single-model pool wants.
	Match map[string]string `yaml:"match,omitempty" json:"match,omitempty"`

	// Requirements gate which GPUs may run this at all.
	Requirements Requirements `yaml:"requirements,omitempty" json:"requirements,omitempty"`

	// Priority ranks templates when several are eligible for one GPU;
	// the highest becomes that GPU's primary.
	Priority int      `yaml:"priority,omitempty" json:"priority,omitempty"`
	Stacking Stacking `yaml:"stacking,omitempty" json:"stacking,omitempty"`

	RunnerCompose RunnerCompose `yaml:"runner_compose,omitempty" json:"runner_compose,omitempty"`

	// Probation and Active are the ladder's two rungs: what a runner
	// gets before it has proved itself, and its ceiling afterwards.
	Probation Probation `yaml:"probation,omitempty" json:"probation,omitempty"`
	Active    Active    `yaml:"active,omitempty" json:"active,omitempty"`

	CommissionBPS uint32 `yaml:"commission_bps,omitempty" json:"commission_bps,omitempty"`
}

type Price struct {
	AmountWei string `yaml:"amount_wei" json:"amount_wei"`
	PerUnits  uint64 `yaml:"per_units,omitempty" json:"per_units,omitempty"`
}

type Capacity struct {
	MaxInFlight int `yaml:"max_in_flight,omitempty" json:"max_in_flight,omitempty"`
	QueueLimit  int `yaml:"queue_limit,omitempty" json:"queue_limit,omitempty"`
}

// Requirements is the hardware gate (plan 0040 §4.3). An empty list
// means "no constraint on this axis", never "nothing qualifies".
type Requirements struct {
	GPUModels       []string `yaml:"gpu_models,omitempty" json:"gpu_models,omitempty"`
	GPUClasses      []string `yaml:"gpu_classes,omitempty" json:"gpu_classes,omitempty"`
	GPUVRAMMinBytes uint64   `yaml:"gpu_vram_min_bytes,omitempty" json:"gpu_vram_min_bytes,omitempty"`
}

// Stacking is the pool's capacity policy, not the member's choice
// (plan 0040 §4.4). A GPU has one primary template; a secondary rides
// alongside only where the operator has allowed that exact combination
// for that GPU class.
type Stacking struct {
	Primary     bool     `yaml:"primary,omitempty" json:"primary,omitempty"`
	SecondaryOn []string `yaml:"secondary_on,omitempty" json:"secondary_on,omitempty"`
}

// RunnerCompose is what the agent needs to start this workload.
//
// Image and Env come straight from plan 0044 §3.2. Command and
// InternalURL are here because the member bundle already needs them: a
// template may override the image's entrypoint, and an operator running
// their own runner supplies a URL instead of an image, in which case
// the bundle ships no service at all.
type RunnerCompose struct {
	Image   string            `yaml:"image,omitempty" json:"image,omitempty"`
	Command []string          `yaml:"command,omitempty" json:"command,omitempty"`
	Env     map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Models  []Model           `yaml:"models,omitempty" json:"models,omitempty"`
	// InternalURL names a runner the operator hosts themselves.
	InternalURL string `yaml:"internal_url,omitempty" json:"internal_url,omitempty"`
}

// Model is a weight file the agent must have on disk before the runner
// can start. Size is declared so the agent can report progress and
// refuse a host that cannot hold it.
type Model struct {
	Name      string `yaml:"name" json:"name"`
	SizeBytes uint64 `yaml:"size_bytes,omitempty" json:"size_bytes,omitempty"`
	Source    string `yaml:"source,omitempty" json:"source,omitempty"`
}

type Probation struct {
	SharePPM    uint64 `yaml:"share_ppm,omitempty" json:"share_ppm,omitempty"`
	MaxInFlight int    `yaml:"max_in_flight,omitempty" json:"max_in_flight,omitempty"`
	MinJobs     int    `yaml:"min_jobs,omitempty" json:"min_jobs,omitempty"`
}

type Active struct {
	ShareCapPPM uint64 `yaml:"share_cap_ppm,omitempty" json:"share_cap_ppm,omitempty"`
}
