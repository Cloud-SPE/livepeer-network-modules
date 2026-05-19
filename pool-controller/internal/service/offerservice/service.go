package offerservice

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/poolscope"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type Mutation struct {
	ID              string          `json:"id"`
	CapabilityID    string          `json:"capability_id"`
	OfferingID      string          `json:"offering_id"`
	InteractionMode string          `json:"interaction_mode"`
	WorkUnit        config.WorkUnit `json:"work_unit"`
	Price           config.Price    `json:"price"`
	Extra           map[string]any  `json:"extra,omitempty"`
	Constraints     map[string]any  `json:"constraints,omitempty"`
	Status          string          `json:"status,omitempty"`
}

func Create(stateRepo *repo.StateRepo, req Mutation) (types.Offer, error) {
	offer, err := offerFromMutation(req)
	if err != nil {
		return types.Offer{}, err
	}
	if err := ensureUniquePublicOffer(stateRepo, offer); err != nil {
		return types.Offer{}, err
	}
	if err := stateRepo.PutOffer(offer); err != nil {
		return types.Offer{}, err
	}
	return offer, nil
}

func Update(stateRepo *repo.StateRepo, current types.Offer, req Mutation) (types.Offer, error) {
	updated, err := updatedOfferFromMutation(current, req)
	if err != nil {
		return types.Offer{}, err
	}
	if err := ensureUniquePublicOffer(stateRepo, updated); err != nil {
		return types.Offer{}, err
	}
	if err := stateRepo.PutOffer(updated); err != nil {
		return types.Offer{}, err
	}
	return updated, nil
}

func offerFromMutation(req Mutation) (types.Offer, error) {
	req.ID = strings.TrimSpace(req.ID)
	req.CapabilityID = strings.TrimSpace(req.CapabilityID)
	req.OfferingID = strings.TrimSpace(req.OfferingID)
	req.InteractionMode = strings.TrimSpace(req.InteractionMode)
	if req.ID == "" {
		return types.Offer{}, fmt.Errorf("id is required")
	}
	if req.CapabilityID == "" || req.OfferingID == "" || req.InteractionMode == "" {
		return types.Offer{}, fmt.Errorf("capability_id, offering_id, and interaction_mode are required")
	}
	if req.WorkUnit.Name == "" || len(req.WorkUnit.Extractor) == 0 {
		return types.Offer{}, fmt.Errorf("work_unit.name and work_unit.extractor are required")
	}
	if req.Price.AmountWei == "" || req.Price.PerUnits == 0 {
		return types.Offer{}, fmt.Errorf("price.amount_wei and price.per_units > 0 are required")
	}
	status := types.OfferStatusActive
	if strings.TrimSpace(req.Status) != "" {
		status = types.OfferStatus(strings.TrimSpace(req.Status))
	}
	offer := types.Offer{
		ID:              req.ID,
		CapabilityID:    req.CapabilityID,
		OfferingID:      req.OfferingID,
		InteractionMode: req.InteractionMode,
		WorkUnit:        req.WorkUnit,
		Price:           req.Price,
		Extra:           req.Extra,
		Constraints:     req.Constraints,
		Status:          status,
	}
	return offer, validateOffer(offer)
}

func updatedOfferFromMutation(current types.Offer, req Mutation) (types.Offer, error) {
	if strings.TrimSpace(req.CapabilityID) != "" {
		current.CapabilityID = strings.TrimSpace(req.CapabilityID)
	}
	if strings.TrimSpace(req.OfferingID) != "" {
		current.OfferingID = strings.TrimSpace(req.OfferingID)
	}
	if strings.TrimSpace(req.InteractionMode) != "" {
		current.InteractionMode = strings.TrimSpace(req.InteractionMode)
	}
	if req.WorkUnit.Name != "" {
		current.WorkUnit = req.WorkUnit
	}
	if req.Price.AmountWei != "" {
		current.Price = req.Price
	}
	if req.Extra != nil {
		current.Extra = req.Extra
	}
	if req.Constraints != nil {
		current.Constraints = req.Constraints
	}
	if strings.TrimSpace(req.Status) != "" {
		current.Status = types.OfferStatus(strings.TrimSpace(req.Status))
	}
	if current.CapabilityID == "" || current.OfferingID == "" || current.InteractionMode == "" {
		return types.Offer{}, fmt.Errorf("capability_id, offering_id, and interaction_mode are required")
	}
	if current.WorkUnit.Name == "" || len(current.WorkUnit.Extractor) == 0 {
		return types.Offer{}, fmt.Errorf("work_unit.name and work_unit.extractor are required")
	}
	if current.Price.AmountWei == "" || current.Price.PerUnits == 0 {
		return types.Offer{}, fmt.Errorf("price.amount_wei and price.per_units > 0 are required")
	}
	return current, validateOffer(current)
}

func validateOffer(offer types.Offer) error {
	if err := poolscope.EnsureSupportedClaim(offer.CapabilityID, offer.InteractionMode); err != nil {
		return err
	}
	switch offer.Status {
	case types.OfferStatusActive, types.OfferStatusDisabled:
	default:
		return fmt.Errorf("status must be active or disabled")
	}
	extractorType, _ := offer.WorkUnit.Extractor["type"].(string)
	if strings.TrimSpace(extractorType) == "" {
		return fmt.Errorf("work_unit.extractor.type is required")
	}
	amount, ok := new(big.Int).SetString(strings.TrimSpace(offer.Price.AmountWei), 10)
	if !ok {
		return fmt.Errorf("price.amount_wei must be a base-10 integer string")
	}
	if amount.Sign() <= 0 {
		return fmt.Errorf("price.amount_wei must be > 0")
	}
	return nil
}

func ensureUniquePublicOffer(stateRepo *repo.StateRepo, offer types.Offer) error {
	if stateRepo == nil {
		return nil
	}
	items, err := stateRepo.ListOffers()
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ID == offer.ID {
			continue
		}
		if item.CapabilityID == offer.CapabilityID && item.OfferingID == offer.OfferingID && item.InteractionMode == offer.InteractionMode {
			return fmt.Errorf("offer %q conflicts with existing offer %q for %s/%s %s", offer.ID, item.ID, offer.CapabilityID, offer.OfferingID, offer.InteractionMode)
		}
	}
	return nil
}
