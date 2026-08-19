package types

import (
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
)

type OfferStatus string

const (
	OfferStatusActive   OfferStatus = "active"
	OfferStatusDisabled OfferStatus = "disabled"
)

// Protocol tag prefixes from livepeer-network-protocol/manifest/schema.json.
// A capability declares exactly one protocol plus the matching axes object.
const (
	ProtocolPaidJobPrefix     = "paid-job/"
	ProtocolPaidSessionPrefix = "paid-session/"

	ProtocolPaidJobV1     = "paid-job/v1"
	ProtocolPaidSessionV1 = "paid-session/v1"
)

// OfferJobAxes carries the paid-job declared axes for an offer. It mirrors
// the manifest's `job` object and the broker host-config's `job` block: the
// only axis today is the transport set the offering serves.
//
// There is deliberately no session-axes counterpart: the Pool member model
// is backend-runtime-only, so pool-controller neither accepts nor renders
// paid-session offerings (see internal/poolscope).
type OfferJobAxes struct {
	Transports []string `json:"transports,omitempty"`
}

type Offer struct {
	ID           string `json:"id"`
	CapabilityID string `json:"capability_id"`
	OfferingID   string `json:"offering_id"`
	// Protocol is the protocol tag ("paid-job/v1"); it replaced the
	// removed v0 interaction-mode field.
	Protocol string `json:"protocol"`
	// Job carries the paid-job declared axes. Optional: offers that
	// predate transport declarations leave it nil and the broker
	// renderer falls back to its documented default.
	Job         *OfferJobAxes   `json:"job,omitempty"`
	WorkUnit    config.WorkUnit `json:"work_unit"`
	Price       config.Price    `json:"price"`
	Extra       map[string]any  `json:"extra,omitempty"`
	Constraints map[string]any  `json:"constraints,omitempty"`
	Status      OfferStatus     `json:"status"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// IsPaidJob reports whether the offer declares a paid-job protocol.
func (o Offer) IsPaidJob() bool {
	return strings.HasPrefix(strings.TrimSpace(o.Protocol), ProtocolPaidJobPrefix)
}

// IsPaidSession reports whether the offer declares a paid-session protocol.
func (o Offer) IsPaidSession() bool {
	return strings.HasPrefix(strings.TrimSpace(o.Protocol), ProtocolPaidSessionPrefix)
}

// JobTransports returns the paid-job transports the offer declares, or nil
// when the offer carries no transport information at all. Callers decide
// what an empty declaration means; brokerrender substitutes its documented
// default.
//
// Lookup order:
//  1. offer.Job.Transports (the canonical, typed home);
//  2. offer.Extra["job"]["transports"] (mirrors the manifest shape when an
//     operator hand-authored the axes into free-form metadata);
//  3. offer.Extra["transports"].
func (o Offer) JobTransports() []string {
	if o.Job != nil && len(o.Job.Transports) > 0 {
		return normalizeStringList(o.Job.Transports)
	}
	if nested, ok := o.Extra["job"].(map[string]any); ok {
		if out := stringListFromAny(nested["transports"]); len(out) > 0 {
			return out
		}
	}
	return stringListFromAny(o.Extra["transports"])
}

func stringListFromAny(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		return normalizeStringList(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return normalizeStringList(out)
	case string:
		return normalizeStringList(strings.Split(typed, ","))
	default:
		return nil
	}
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
