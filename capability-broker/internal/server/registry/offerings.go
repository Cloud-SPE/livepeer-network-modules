// Package registry implements the unpaid registry endpoints:
//
//	GET /registry/offerings  — capability inventory (manifest payload sans signature)
//	GET /registry/health     — normalized per-tuple live-health snapshots
//	GET /healthz             — process liveness probe
//
// Per the spec, the broker only publishes the bare offerings payload; signing
// is the orch-coordinator's job. The orch-coordinator scrapes this endpoint,
// composes the rooted manifest, hand-carries it to secure-orch for signing,
// and atomic-swap publishes at /.well-known/livepeer-registry.json.
package registry

import (
	"encoding/json"
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/version"
)

type ExtraOverlaySource interface {
	ExtraFor(capabilityID, offeringID string) map[string]any
}

// OfferingsHandler returns the configured capability list as the manifest
// payload (sans signature and worker_url — the orch-coordinator fills in
// worker_url based on which broker it scraped).
//
// The response shape conforms to the manifest payload at
// livepeer-network-protocol/manifest/schema.json (#/$defs/manifest).
func OfferingsHandler(cfg *config.Config, overlays ExtraOverlaySource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		payload := BuildOfferings(cfg, overlays)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(payload)
	}
}

type offeringsPayload struct {
	// SpecVersion is the protocol module's VERSION the broker was built
	// against (broker-admin §7). The coordinator refuses to merge
	// brokers whose major differs from its own.
	SpecVersion    string                  `json:"spec_version"`
	OrchEthAddress string                  `json:"orch_eth_address"`
	OffersRevision string                  `json:"offers_revision,omitempty"`
	Capabilities   []offeringsCapabilityV1 `json:"capabilities"`
}

type offeringsCapabilityV1 struct {
	CapabilityID string `json:"capability_id"`
	OfferingID   string `json:"offering_id"`
	Protocol     string `json:"protocol"`
	// Job and Session carry the declared axes through to the
	// coordinator, which signs them into the manifest verbatim. A
	// paid-* offering without its axes object produces a manifest that
	// fails schema validation, so exactly one of these is always set.
	Job             *offeringsJobAxes     `json:"job,omitempty"`
	Session         *offeringsSessionAxes `json:"session,omitempty"`
	WorkUnit        offeringsWorkUnit     `json:"work_unit"`
	PricePerUnitWei string                `json:"price_per_unit_wei"`
	PerUnits        uint64                `json:"per_units"`
	Extra           map[string]any        `json:"extra,omitempty"`
	// Constraints is always emitted (no omitempty). Resolvers downstream
	// hash the canonical constraints bytes; an absent block previously
	// produced a nil constraint_fingerprint that failed request-path
	// filtering. An empty operator config marshals as `"constraints":{}`.
	Constraints map[string]any `json:"constraints"`
}

type offeringsWorkUnit struct {
	Name string `json:"name"`
	// Estimator describes how a CLIENT can compute a funding ceiling
	// before the work runs, for the offerings where that is possible.
	//
	// Extractors are otherwise deliberately unadvertised — how a seller
	// counts is its own business and no counterparty gates on it
	// (offering-axes.md). This is the exception: a caller that must
	// reserve funds up front has to reach the same number the seller
	// will bill, and for a multipart upload it cannot derive that from
	// the request parameters. So the offering names the estimator, its
	// rounding, and its exactness requirement, and the client runs the
	// published implementation rather than inventing a parser.
	Estimator *offeringsEstimator `json:"estimator,omitempty"`
}

type offeringsEstimator struct {
	// ID is the versioned estimator name, e.g.
	// "multipart-audio-duration/v1". A client MUST refuse an id it does
	// not implement rather than guess a ceiling.
	ID string `json:"id"`
	// Rounding is how a fractional measurement becomes billable units.
	Rounding string `json:"rounding"`
	// Exactness states what an implementation must do when it cannot
	// measure exactly. "exact-or-reject" means it MUST refuse rather
	// than return an estimate: a ceiling that reads low underfunds real
	// work and one that reads high overcharges, so neither is guessed.
	Exactness string `json:"exactness"`
	// Package is the canonical client implementation.
	Package string `json:"package,omitempty"`
	// Fixtures is the shared conformance set both sides run, so a
	// disagreement is a test failure rather than a refused exchange.
	Fixtures string `json:"fixtures,omitempty"`
}

// clientEstimators maps an extractor type to the estimator a client can
// run itself. Absent means the offering advertises none, which is the
// default and correct for extractors whose inputs the client does not
// have.
var clientEstimators = map[string]offeringsEstimator{
	"multipart-audio-duration": {
		ID:        "multipart-audio-duration/v1",
		Rounding:  "ceil-to-whole-seconds",
		Exactness: "exact-or-reject",
		// No Package. The field names a canonical client implementation
		// a consumer can install, and there is no longer one to name:
		// implementations are independently owned, and the package this
		// used to advertise was never published to npm — so a caller
		// that trusted the field and ran `npm i` got a 404.
		//
		// What actually defines the contract is ID, Rounding, Exactness
		// and the fixtures both sides run. An unresolvable pointer to a
		// fourth thing adds nothing and costs a debugging session.
		Fixtures: "livepeer-network-protocol/extractors/fixtures/multipart-audio-duration-v1",
	},
}

// estimatorFor returns the advertised estimator for an extractor config.
func estimatorFor(extractor map[string]any) *offeringsEstimator {
	t, _ := extractor["type"].(string)
	est, ok := clientEstimators[t]
	if !ok {
		return nil
	}
	return &est
}

// offeringsJobAxes / offeringsSessionAxes mirror the manifest schema's
// axes objects (livepeer-network-protocol/manifest/schema.json), not the
// broker's host-config shape — this is the advertisement, so it speaks
// the published vocabulary.
type offeringsJobAxes struct {
	Transports []string `json:"transports"`
}

type offeringsSessionAxes struct {
	DescriptorSchema     string              `json:"descriptor_schema"`
	MaxRotations         int                 `json:"max_rotations,omitempty"`
	Attachment           string              `json:"attachment,omitempty"`
	Metering             string              `json:"metering"`
	Refill               string              `json:"refill,omitempty"`
	Heartbeat            *offeringsHeartbeat `json:"heartbeat,omitempty"`
	Lease                *offeringsLease     `json:"lease,omitempty"`
	ToleranceBandPct     float64             `json:"tolerance_band_pct,omitempty"`
	RunwayIncrementUnits int64               `json:"runway_increment_units,omitempty"`
	// SessionParamsSchema is the runner's own description of the params
	// it expects, relayed verbatim so gateways can validate before
	// opening. The broker never enforces it.
	SessionParamsSchema json.RawMessage `json:"session_params_schema,omitempty"`
}

type offeringsHeartbeat struct {
	IntervalSeconds int `json:"interval_seconds,omitempty"`
	MissedThreshold int `json:"missed_threshold,omitempty"`
}

type offeringsLease struct {
	Policy     string `json:"policy,omitempty"`
	MaxSeconds int    `json:"max_seconds,omitempty"`
}

// axesFor maps a host-config capability to its advertised axes object.
func axesFor(c config.Capability) (*offeringsJobAxes, *offeringsSessionAxes) {
	if c.Job != nil {
		return &offeringsJobAxes{Transports: c.Job.Transports}, nil
	}
	if c.Session == nil {
		return nil, nil
	}
	sess := &offeringsSessionAxes{
		SessionParamsSchema:  c.Session.SessionParamsSchema,
		DescriptorSchema:     c.Session.DescriptorSchema,
		MaxRotations:         c.Session.MaxRotations,
		Attachment:           c.Session.AdvertisedAttachment(),
		Metering:             c.Session.AdvertisedMetering(),
		Refill:               c.Session.AdvertisedRefill(),
		ToleranceBandPct:     c.Session.ToleranceBandPct,
		RunwayIncrementUnits: c.Session.RunwayIncrementUnits,
	}
	if hb := c.Session.Heartbeat; hb.IntervalSeconds > 0 || hb.MissedThreshold > 0 {
		sess.Heartbeat = &offeringsHeartbeat{
			IntervalSeconds: hb.IntervalSeconds,
			MissedThreshold: hb.MissedThreshold,
		}
	}
	// Host config carries a flat cap; the manifest models lease as an
	// object with an explicit policy.
	if c.Session.LeaseMaxSeconds > 0 || c.Session.LeasePolicy != "" {
		sess.Lease = &offeringsLease{
			Policy:     c.Session.AdvertisedLeasePolicy(),
			MaxSeconds: c.Session.LeaseMaxSeconds,
		}
	}
	return nil, sess
}

func BuildOfferings(cfg *config.Config, overlays ExtraOverlaySource) offeringsPayload {
	out := offeringsPayload{
		SpecVersion:    version.VERSION,
		OrchEthAddress: cfg.Identity.OrchEthAddress,
		Capabilities:   make([]offeringsCapabilityV1, 0, len(cfg.Capabilities)),
	}
	seen := map[string]struct{}{}
	for _, c := range cfg.Capabilities {
		key := c.ID + "|" + c.OfferingID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		extra := mergeExtraMaps(c.Extra, overlayFor(overlays, c.ID, c.OfferingID))
		constraints := c.Constraints
		if constraints == nil {
			constraints = map[string]any{}
		}
		jobAxes, sessionAxes := axesFor(c)
		out.Capabilities = append(out.Capabilities, offeringsCapabilityV1{
			CapabilityID: c.ID,
			OfferingID:   c.OfferingID,
			Protocol:     c.Protocol,
			Job:          jobAxes,
			Session:      sessionAxes,
			WorkUnit: offeringsWorkUnit{
				Name:      c.WorkUnit.Name,
				Estimator: estimatorFor(c.WorkUnit.Extractor),
			},
			PricePerUnitWei: c.Price.AmountWei,
			PerUnits:        c.Price.PerUnits,
			Extra:           extra,
			Constraints:     constraints,
		})
	}
	return out
}

func buildOfferings(cfg *config.Config, overlays ExtraOverlaySource) offeringsPayload {
	return BuildOfferings(cfg, overlays)
}

func overlayFor(src ExtraOverlaySource, capabilityID, offeringID string) map[string]any {
	if src == nil {
		return nil
	}
	return src.ExtraFor(capabilityID, offeringID)
}

func mergeExtraMaps(base, overlay map[string]any) map[string]any {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	out := cloneMap(base)
	if out == nil {
		out = map[string]any{}
	}
	for key, value := range overlay {
		if nestedOverlay, ok := value.(map[string]any); ok {
			nestedBase, _ := out[key].(map[string]any)
			out[key] = mergeExtraMaps(nestedBase, nestedOverlay)
			continue
		}
		out[key] = value
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		nested, ok := v.(map[string]any)
		if ok {
			out[k] = cloneMap(nested)
			continue
		}
		out[k] = v
	}
	return out
}
