package brokerpush

import (
	"sort"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// BuildOffersFromCatalog turns the pool's enabled templates into the
// offer set a broker is told to serve (plan 0044 §3.2).
//
// A template the pool has never adopted is not pushed at all: it is a
// workload this build knows how to run, not one this pool sells. A
// template the pool has explicitly DISABLED is pushed with disabled
// set, which is a different thing — the broker keeps the offer and its
// frozen shape but neither advertises nor dispatches it (broker-admin
// §4.4). Dropping it from the set instead would delete the offer, and
// with it the record of which runner shape was ever certified against
// it, so re-enabling would silently start a fresh freeze.
//
// What travels is the operator's half only: price, capacity, extra,
// the runner selector, and the certification recipe. Transports, work
// units and extractors are the runner's to declare and the broker's to
// freeze (plan 0043 §8).
func BuildOffersFromCatalog(catalog []templates.Template, overrides []types.TemplateOverride) []brokeradmin.OfferPush {
	byID := make(map[string]types.TemplateOverride, len(overrides))
	for _, override := range overrides {
		byID[override.TemplateID] = override
	}
	out := make([]brokeradmin.OfferPush, 0, len(catalog))
	for _, tmpl := range catalog {
		override, adopted := byID[tmpl.ID]
		if !adopted {
			continue
		}
		push := brokeradmin.OfferPush{
			OfferingID:      tmpl.OfferingID,
			Capability:      tmpl.Capability,
			Protocol:        tmpl.Protocol,
			Match:           matchForTemplate(tmpl, override),
			Price:           priceFor(tmpl, override),
			Extra:           mergeExtra(tmpl.Extra, override.Extra),
			Constraints:     cloneAny(tmpl.Constraints),
			ExtraFromRunner: append([]string(nil), tmpl.ExtraFromRunner...),
			Certification:   certificationFor(tmpl),
			Disabled:        !override.Enabled,
		}
		push.SessionPolicy = sessionPolicyFor(tmpl)
		if tmpl.Capacity.MaxInFlight > 0 || tmpl.Capacity.QueueLimit > 0 {
			push.Capacity = &brokeradmin.OfferPushCapacity{
				MaxInFlight: tmpl.Capacity.MaxInFlight,
				QueueLimit:  tmpl.Capacity.QueueLimit,
			}
		}
		out = append(out, push)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OfferingID < out[j].OfferingID })
	return out
}

// priceFor applies the pool's override, falling back to the catalog's
// suggestion. per_units 0 would make the broker's arithmetic divide by
// nothing, so it is normalised here rather than at the far end.
func priceFor(tmpl templates.Template, override types.TemplateOverride) brokeradmin.OfferPushPrice {
	amount, per := tmpl.PriceDefault.AmountWei, tmpl.PriceDefault.PerUnits
	if override.Price != nil {
		amount, per = override.Price.AmountWei, override.Price.PerUnits
	}
	if per == 0 {
		per = 1
	}
	return brokeradmin.OfferPushPrice{AmountWei: amount, PerUnits: per}
}

// matchForTemplate prefers what the template declares. Falling back to
// the identity in extra keeps a single-model template working without
// stating the same model twice, and an offer that names no identity at
// all takes any runner serving the capability.
func matchForTemplate(tmpl templates.Template, override types.TemplateOverride) map[string]string {
	if len(tmpl.Match) > 0 {
		out := make(map[string]string, len(tmpl.Match))
		for k, v := range tmpl.Match {
			out[k] = v
		}
		return out
	}
	extra := mergeExtra(tmpl.Extra, override.Extra)
	if model := identityValue(extra, "openai", "model"); model != "" {
		return map[string]string{"identity.openai.model": model}
	}
	return nil
}

// mergeExtra layers the pool's metadata over the catalog's. A key set
// on both takes the pool's value: an override exists precisely to
// disagree with the catalog.
//
// Only the top level is copied, so a nested map is shared with the
// loaded catalog and the stored override. That is safe because the
// result is serialised to JSON and discarded — but anything that starts
// mutating a pushed offer in place must deep-copy first, or it would be
// editing the catalog every pool in this build reads.
func mergeExtra(base, overlay map[string]any) map[string]any {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	out := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

// certificationFor converts the template's recipe to the wire shape.
//
// Required is a pointer on the wire because the broker defaults an
// omitted one to true. A template that marks a step optional means it,
// so the value is always sent rather than left to a default that would
// invert it.
func certificationFor(tmpl templates.Template) []brokeradmin.OfferPushCertStep {
	if len(tmpl.Certification) == 0 {
		return nil
	}
	out := make([]brokeradmin.OfferPushCertStep, 0, len(tmpl.Certification))
	for _, step := range tmpl.Certification {
		required := step.Required
		out = append(out, brokeradmin.OfferPushCertStep{
			Name:      step.Name,
			Type:      step.Type,
			Required:  &required,
			TimeoutMS: step.TimeoutMS,
			Config:    step.Config,
		})
	}
	return out
}

// sessionPolicyFor carries the operator's session axes to the broker.
//
// Authored and dropped would be the worst outcome: an operator sets a
// fixed lease, the template validates, the push succeeds, and the
// broker serves the default because nothing carried it across.
func sessionPolicyFor(tmpl templates.Template) *brokeradmin.OfferPushSessionPolicy {
	p := tmpl.SessionPolicy
	if p == nil {
		return nil
	}
	out := &brokeradmin.OfferPushSessionPolicy{
		Attachment:           p.Attachment,
		Refill:               p.Refill,
		LeasePolicy:          p.LeasePolicy,
		LeaseMaxSeconds:      p.LeaseMaxSeconds,
		BurnRatePerSec:       p.BurnRatePerSec,
		MinRunwayUnits:       p.MinRunwayUnits,
		MaxRotations:         p.MaxRotations,
		ToleranceBandPct:     p.ToleranceBandPct,
		RunwayIncrementUnits: p.RunwayIncrementUnits,
	}
	if p.Heartbeat.IntervalSeconds > 0 || p.Heartbeat.MissedThreshold > 0 {
		out.Heartbeat = &brokeradmin.OfferPushHeartbeat{
			IntervalSeconds: p.Heartbeat.IntervalSeconds,
			MissedThreshold: p.Heartbeat.MissedThreshold,
		}
	}
	return out
}

// cloneAny copies the advertised constraints so a pushed offer does not
// share a map with the loaded catalog.
func cloneAny(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
