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

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
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
		payload := buildOfferings(cfg, overlays)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(payload)
	}
}

type offeringsPayload struct {
	OrchEthAddress string                  `json:"orch_eth_address"`
	Capabilities   []offeringsCapabilityV1 `json:"capabilities"`
}

type offeringsCapabilityV1 struct {
	CapabilityID    string            `json:"capability_id"`
	OfferingID      string            `json:"offering_id"`
	InteractionMode string            `json:"interaction_mode"`
	WorkUnit        offeringsWorkUnit `json:"work_unit"`
	PricePerUnitWei string            `json:"price_per_unit_wei"`
	PerUnits        uint64            `json:"per_units"`
	Extra           map[string]any    `json:"extra,omitempty"`
	Constraints     map[string]any    `json:"constraints,omitempty"`
}

type offeringsWorkUnit struct {
	Name string `json:"name"`
}

func buildOfferings(cfg *config.Config, overlays ExtraOverlaySource) offeringsPayload {
	out := offeringsPayload{
		OrchEthAddress: cfg.Identity.OrchEthAddress,
		Capabilities:   make([]offeringsCapabilityV1, 0, len(cfg.Capabilities)),
	}
	for _, c := range cfg.Capabilities {
		extra := mergeExtraMaps(c.Extra, overlayFor(overlays, c.ID, c.OfferingID))
		out.Capabilities = append(out.Capabilities, offeringsCapabilityV1{
			CapabilityID:    c.ID,
			OfferingID:      c.OfferingID,
			InteractionMode: c.InteractionMode,
			WorkUnit:        offeringsWorkUnit{Name: c.WorkUnit.Name},
			PricePerUnitWei: c.Price.AmountWei,
			PerUnits:        c.Price.PerUnits,
			Extra:           extra,
			Constraints:     c.Constraints,
		})
	}
	return out
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
