package registry

import (
	"encoding/json"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
)

// FrozenShape is the runner-derived half of an advertised offer tuple:
// the offer engine's frozen projection plus the relayed runner facts
// that ride with it.
type FrozenShape struct {
	Projection               runnerattach.Projection
	SessionParamsSchema      json.RawMessage
	HeartbeatIntervalSeconds int
}

// OfferTuple composes one advertised capability tuple from an offer's
// operator fields and its frozen shape (plan 0043 §3.4: the payload is a
// pure function of the offer set and the frozen shapes — never of live
// runners). Returns nil when the shape cannot be advertised.
func OfferTuple(o config.Offer, shape FrozenShape) *offeringsCapabilityV1 {
	proj := shape.Projection
	t := &offeringsCapabilityV1{
		CapabilityID:    o.Capability,
		OfferingID:      o.OfferingID,
		Protocol:        o.Protocol,
		PricePerUnitWei: o.Price.AmountWei,
		PerUnits:        o.Price.PerUnits,
		WorkUnit: offeringsWorkUnit{
			Name:      proj.WorkUnit.Name,
			Estimator: estimatorFor(proj.WorkUnit.Extractor),
		},
		Constraints: o.Constraints,
	}
	if t.Constraints == nil {
		t.Constraints = map[string]any{}
	}
	switch {
	case strings.HasPrefix(o.Protocol, "paid-job/"):
		t.Job = &offeringsJobAxes{Transports: proj.Transports}
	case strings.HasPrefix(o.Protocol, "paid-session/"):
		sess := &offeringsSessionAxes{
			Metering:            proj.Metering,
			SessionParamsSchema: shape.SessionParamsSchema,
		}
		// The manifest tuple carries ONE descriptor schema. A runner may
		// declare several; the first (sorted) is advertised. Offers that
		// need another schema advertised are a second offer. Tracked as
		// debt in plan 0043.
		if len(proj.DescriptorSchemas) > 0 {
			sess.DescriptorSchema = proj.DescriptorSchemas[0]
		}
		if p := o.SessionPolicy; p != nil {
			sess.Attachment = orDefault(p.Attachment, "external")
			sess.Refill = orDefault(p.Refill, "extensible")
			sess.MaxRotations = p.MaxRotations
			sess.ToleranceBandPct = p.ToleranceBandPct
			sess.RunwayIncrementUnits = p.RunwayIncrementUnits
			if p.Heartbeat.IntervalSeconds > 0 || p.Heartbeat.MissedThreshold > 0 {
				sess.Heartbeat = &offeringsHeartbeat{
					IntervalSeconds: p.Heartbeat.IntervalSeconds,
					MissedThreshold: p.Heartbeat.MissedThreshold,
				}
			}
			if p.LeaseMaxSeconds > 0 || p.LeasePolicy != "" {
				sess.Lease = &offeringsLease{Policy: orDefault(p.LeasePolicy, "funding-tracking"), MaxSeconds: p.LeaseMaxSeconds}
			}
		} else {
			sess.Attachment = "external"
			sess.Refill = "extensible"
		}
		if sess.Heartbeat == nil && shape.HeartbeatIntervalSeconds > 0 {
			// The freezing runner's advisory cadence, advertised when the
			// operator's policy leaves it unset.
			sess.Heartbeat = &offeringsHeartbeat{IntervalSeconds: shape.HeartbeatIntervalSeconds}
		}
		t.Session = sess
	default:
		return nil
	}
	// extra = operator extra + frozen identity (dotted keys expanded to
	// nested objects) + promoted x-* keys. A collision favours the
	// runner-derived value: it is the frozen truth the operator selected.
	extra := cloneMap(o.Extra)
	if extra == nil {
		extra = map[string]any{}
	}
	for k, v := range proj.Identity {
		setDotted(extra, k, v)
	}
	for k, raw := range proj.Promoted {
		var v any
		if err := json.Unmarshal(raw, &v); err == nil {
			extra[k] = v
		}
	}
	if len(extra) > 0 {
		t.Extra = extra
	}
	return t
}

// setDotted expands "openai.model" into extra{openai:{model:v}}.
func setDotted(m map[string]any, key string, value any) {
	parts := strings.Split(key, ".")
	cur := m
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = value
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
