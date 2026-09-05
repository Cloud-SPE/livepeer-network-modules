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

import "strings"

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
	Extra map[string]any `yaml:"extra,omitempty" json:"extra,omitempty"`

	// Constraints are the advertised axes a buyer routes on — region,
	// tier, GPU vendor. They reach the manifest, so a gateway choosing
	// between orchestrators sees them.
	//
	// Distinct from Extra, which describes what is being sold. A
	// constraint is something the caller filters by: losing `region`
	// does not make the offering wrong, it makes it unfindable by
	// anyone who cares where their work runs.
	Constraints     map[string]any `yaml:"constraints,omitempty" json:"constraints,omitempty"`
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

	// SessionPolicy is the operator's half of a paid-session offering
	// (offering-axes.md §3). It is meaningless on paid-job and rejected
	// there: a job has no lease to bound and no heartbeat to miss.
	//
	// Without this a pool could author a session template and the
	// broker would receive an offer with no axes at all, silently
	// taking every default — so a streaming workload that needs a fixed
	// lease or a faster heartbeat could be declared and would not be
	// honoured.
	SessionPolicy *SessionPolicy `yaml:"session_policy,omitempty" json:"session_policy,omitempty"`

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
	// CPUClasses admits cpu units (plan 0047): core tiers, cpu-8 .. cpu-64.
	// A template that lists none never places on a socket, and a
	// template that lists only these never places on a card — the two
	// kinds do not compete for one another's units.
	CPUClasses []string `yaml:"cpu_classes,omitempty" json:"cpu_classes,omitempty"`
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
	// Image is the runner image PER VENDOR: the same product is served by
	// a different build on an NVIDIA card than on an Intel one, and the
	// controller picks at desired-state render, where it knows the card.
	//
	// A map rather than a templated string on purpose (plan 0045 §4). A
	// template with no image for a vendor must make that card ineligible
	// at placement, which a map makes checkable when the catalog loads;
	// a string like "{{gpu.vendor}}" always produces a name, and it is
	// wrong on a member's host rather than in review. Keys are gpu
	// vendors; a template that names none renders no service.
	Image   map[string]string `yaml:"image,omitempty" json:"image,omitempty"`
	Command []string          `yaml:"command,omitempty" json:"command,omitempty"`
	Env     map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Models  []Model           `yaml:"models,omitempty" json:"models,omitempty"`
	// InternalURL names a runner the operator hosts themselves.
	InternalURL string `yaml:"internal_url,omitempty" json:"internal_url,omitempty"`
	// RTMPPort is the container port an RTMP ingest listens on (plan
	// 0046 §2.7). The agent's edge terminates RTMPS on the host's
	// published port and forwards to it; the runner receives its public
	// address as LIVEPEER_PUBLIC_RTMP_URL. Zero: no ingest.
	RTMPPort int `yaml:"rtmp_port,omitempty" json:"rtmp_port,omitempty"`
}

// RTMPSPublicPort is the host port the agent's edge terminates RTMPS on
// (plan 0046 §2.7). One per host: the live class's stance is one
// template per card and the media router multiplexes streams by key.
const RTMPSPublicPort = 1936

// ImageFor is the image this template runs on a card of the given
// vendor, or empty when it has none — which placement treats as "this
// card cannot run it", never as "run it anyway".
func (rc RunnerCompose) ImageFor(vendor string) string {
	return strings.TrimSpace(rc.Image[strings.ToLower(strings.TrimSpace(vendor))])
}

// ImageForClass is ImageFor with a class override: a key of the form
// `<vendor>/<class>` names a build for that one class and wins over the
// vendor's default. The case it exists for is a CUDA generation the
// vendor's default build no longer targets — a GTX 1080 needs a
// cu126 variant while every newer card runs the cu128 default — and
// that is the template author's knowledge of the image, which is why
// it is the image map's business and not a second template's.
func (rc RunnerCompose) ImageForClass(vendor, class string) string {
	vendor = strings.ToLower(strings.TrimSpace(vendor))
	if class = strings.ToLower(strings.TrimSpace(class)); class != "" {
		if img := strings.TrimSpace(rc.Image[vendor+"/"+class]); img != "" {
			return img
		}
	}
	if img := rc.ImageFor(vendor); img != "" {
		return img
	}
	// A build that runs anywhere: the live runner is ubuntu + ffmpeg +
	// a media router with no vendor stage.
	return strings.TrimSpace(rc.Image["any"])
}

// SplitImageKey parses an image-map key into its vendor and optional
// class.
func SplitImageKey(key string) (vendor, class string) {
	vendor, class, _ = strings.Cut(strings.ToLower(strings.TrimSpace(key)), "/")
	return vendor, class
}

// HasImage reports whether the template ships a runner at all.
func (rc RunnerCompose) HasImage() bool { return len(rc.Image) > 0 }

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

// SessionPolicy mirrors the broker's offer-side session axes. The pool
// owns these; what the runner declares (descriptor schemas, metering,
// its own heartbeat cadence) is frozen from the attach document.
type SessionPolicy struct {
	Attachment           string           `yaml:"attachment,omitempty" json:"attachment,omitempty"`
	Refill               string           `yaml:"refill,omitempty" json:"refill,omitempty"`
	LeasePolicy          string           `yaml:"lease_policy,omitempty" json:"lease_policy,omitempty"`
	LeaseMaxSeconds      int              `yaml:"lease_max_seconds,omitempty" json:"lease_max_seconds,omitempty"`
	BurnRatePerSec       float64          `yaml:"burn_rate_per_second,omitempty" json:"burn_rate_per_second,omitempty"`
	MinRunwayUnits       int64            `yaml:"min_runway_units,omitempty" json:"min_runway_units,omitempty"`
	MaxRotations         int              `yaml:"max_rotations,omitempty" json:"max_rotations,omitempty"`
	ToleranceBandPct     float64          `yaml:"tolerance_band_pct,omitempty" json:"tolerance_band_pct,omitempty"`
	RunwayIncrementUnits int64            `yaml:"runway_increment_units,omitempty" json:"runway_increment_units,omitempty"`
	Heartbeat            SessionHeartbeat `yaml:"heartbeat,omitempty" json:"heartbeat,omitempty"`
}

type SessionHeartbeat struct {
	IntervalSeconds int `yaml:"interval_seconds,omitempty" json:"interval_seconds,omitempty"`
	MissedThreshold int `yaml:"missed_threshold,omitempty" json:"missed_threshold,omitempty"`
}
