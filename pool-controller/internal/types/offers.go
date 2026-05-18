package types

import (
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
)

type OfferStatus string

const (
	OfferStatusActive   OfferStatus = "active"
	OfferStatusDisabled OfferStatus = "disabled"
)

type Offer struct {
	ID              string          `json:"id"`
	CapabilityID    string          `json:"capability_id"`
	OfferingID      string          `json:"offering_id"`
	InteractionMode string          `json:"interaction_mode"`
	WorkUnit        config.WorkUnit `json:"work_unit"`
	Price           config.Price    `json:"price"`
	Extra           map[string]any  `json:"extra,omitempty"`
	Constraints     map[string]any  `json:"constraints,omitempty"`
	Status          OfferStatus     `json:"status"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}
