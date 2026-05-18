package repo

import (
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func (r *StateRepo) PutOffer(offer types.Offer) error {
	now := time.Now().UTC()
	if offer.CreatedAt.IsZero() {
		offer.CreatedAt = now
	}
	offer.UpdatedAt = now
	if offer.Status == "" {
		offer.Status = types.OfferStatusActive
	}
	return putJSON(r, offersBucket, offer.ID, offer)
}

func (r *StateRepo) GetOffer(id string) (types.Offer, error) {
	var out types.Offer
	err := getJSON(r, offersBucket, id, &out)
	return out, err
}

func (r *StateRepo) ListOffers() ([]types.Offer, error) {
	return listJSON(r, offersBucket, func(left, right types.Offer) bool {
		if left.CapabilityID != right.CapabilityID {
			return left.CapabilityID < right.CapabilityID
		}
		if left.OfferingID != right.OfferingID {
			return left.OfferingID < right.OfferingID
		}
		return left.ID < right.ID
	})
}

func (r *StateRepo) SetOfferStatus(id string, status types.OfferStatus) error {
	item, err := r.GetOffer(id)
	if err != nil {
		return err
	}
	item.Status = status
	item.UpdatedAt = time.Now().UTC()
	return putJSON(r, offersBucket, item.ID, item)
}
