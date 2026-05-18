package legacyimport

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type Result struct {
	Offers      []types.Offer
	Members     []types.MemberRecord
	Backends    []types.MemberBackend
	Assignments []types.Assignment
}

func Build(cfg *config.Config, now time.Time) (Result, error) {
	if cfg == nil {
		return Result{}, fmt.Errorf("config is nil")
	}
	now = normalizeNow(now)
	offersByKey := make(map[string]types.Offer)
	result := Result{
		Offers:      make([]types.Offer, 0),
		Members:     make([]types.MemberRecord, 0, len(cfg.Members)),
		Backends:    make([]types.MemberBackend, 0),
		Assignments: make([]types.Assignment, 0),
	}

	for _, member := range cfg.Members {
		memberID := stableID("member", member.EthAddress)
		payoutMode := member.PayoutMode
		if payoutMode == "" {
			payoutMode = "onchain"
		}
		result.Members = append(result.Members, types.MemberRecord{
			ID:          memberID,
			EthAddress:  member.EthAddress,
			DisplayName: member.DisplayName,
			PayoutMode:  payoutMode,
			Status:      types.MemberStatusActive,
			CreatedAt:   now,
			UpdatedAt:   now,
		})

		for _, backend := range member.Backends {
			backendID := stableID("backend", member.EthAddress, backend.ID)
			result.Backends = append(result.Backends, types.MemberBackend{
				ID:                  backendID,
				MemberID:            memberID,
				Transport:           backend.Transport,
				URL:                 backend.URL,
				Auth:                backend.Auth,
				HealthProbe:         mergedHealthProbe(backend.Offerings),
				ClaimedCapabilities: claimedOffersForBackend(backend.Offerings),
				VerificationStatus:  types.VerificationUnknown,
				Status:              types.BackendStatusActive,
				CreatedAt:           now,
				UpdatedAt:           now,
			})

			for _, offering := range backend.Offerings {
				key := offerKey(offering)
				offer, ok := offersByKey[key]
				if !ok {
					offer = types.Offer{
						ID:              stableID("offer", offering.CapabilityID, offering.OfferingID, offering.InteractionMode, canonicalMapKey(offering.Extra), canonicalMapKey(offering.Constraints), offering.Price.AmountWei, fmt.Sprintf("%d", offering.Price.PerUnits)),
						CapabilityID:    offering.CapabilityID,
						OfferingID:      offering.OfferingID,
						InteractionMode: offering.InteractionMode,
						WorkUnit:        offering.WorkUnit,
						Price:           offering.Price,
						Extra:           cloneMap(offering.Extra),
						Constraints:     cloneMap(offering.Constraints),
						Status:          types.OfferStatusActive,
						CreatedAt:       now,
						UpdatedAt:       now,
					}
					offersByKey[key] = offer
					result.Offers = append(result.Offers, offer)
				}
				result.Assignments = append(result.Assignments, types.Assignment{
					ID:              stableID("assignment", offer.ID, backendID),
					OfferID:         offer.ID,
					MemberBackendID: backendID,
					Status:          types.AssignmentStatusActive,
					CreatedAt:       now,
					UpdatedAt:       now,
				})
			}
		}
	}

	sort.Slice(result.Offers, func(i, j int) bool {
		if result.Offers[i].CapabilityID != result.Offers[j].CapabilityID {
			return result.Offers[i].CapabilityID < result.Offers[j].CapabilityID
		}
		if result.Offers[i].OfferingID != result.Offers[j].OfferingID {
			return result.Offers[i].OfferingID < result.Offers[j].OfferingID
		}
		return result.Offers[i].ID < result.Offers[j].ID
	})
	return result, nil
}

func Persist(state *repo.StateRepo, built Result, actor string, now time.Time) error {
	if state == nil {
		return fmt.Errorf("state repo is nil")
	}
	now = normalizeNow(now)
	for _, offer := range built.Offers {
		if err := state.PutOffer(offer); err != nil {
			return err
		}
	}
	for _, member := range built.Members {
		if err := state.PutMember(member); err != nil {
			return err
		}
	}
	for _, backend := range built.Backends {
		if err := state.PutMemberBackend(backend); err != nil {
			return err
		}
	}
	for _, assignment := range built.Assignments {
		if err := state.PutAssignment(assignment); err != nil {
			return err
		}
	}
	return state.AppendAuditEvent(types.AuditEvent{
		Kind:         "legacy_import",
		OccurredAt:   now,
		Actor:        actor,
		ResourceType: "pool_controller",
		Details: map[string]any{
			"offers":      len(built.Offers),
			"members":     len(built.Members),
			"backends":    len(built.Backends),
			"assignments": len(built.Assignments),
		},
	})
}

func normalizeNow(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}

func offerKey(offering config.Offering) string {
	return strings.Join([]string{
		offering.CapabilityID,
		offering.OfferingID,
		offering.InteractionMode,
		canonicalMapKey(offering.Extra),
		canonicalMapKey(offering.Constraints),
		offering.Price.AmountWei,
		fmt.Sprintf("%d", offering.Price.PerUnits),
	}, "|")
}

func stableID(parts ...string) string {
	h := sha1.Sum([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:8])
}

func canonicalMapKey(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, ",")
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func claimedOffersForBackend(offerings []config.Offering) []types.ClaimedOffer {
	out := make([]types.ClaimedOffer, 0, len(offerings))
	for _, offering := range offerings {
		out = append(out, types.ClaimedOffer{
			CapabilityID:    offering.CapabilityID,
			OfferingID:      offering.OfferingID,
			InteractionMode: offering.InteractionMode,
			Extra:           cloneMap(offering.Extra),
			Constraints:     cloneMap(offering.Constraints),
		})
	}
	return out
}

func mergedHealthProbe(offerings []config.Offering) config.HealthProbe {
	for _, offering := range offerings {
		if offering.Health.Probe.Type != "" {
			return offering.Health.Probe
		}
	}
	return config.HealthProbe{}
}
