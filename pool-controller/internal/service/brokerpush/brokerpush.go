// Package brokerpush sends the pool's own state to a broker over the
// admin API (plan 0043 item 17).
//
// The controller used to render the broker's entire host-config file and
// have the broker reload it, which meant every member change rewrote a
// file describing runners the controller could only guess at. It now
// pushes exactly what it owns:
//
//   - the offer set: what the pool sells, at what price, with what
//     capacity, gated by which certification steps;
//   - the credentials that may attach, as hashes only.
//
// And reads back what the broker owns: which runners attached, what
// hardware they declared, and how they certified. Runner facts never
// travel in this direction.
package brokerpush

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// State is the controller state a push is built from.
type State struct {
	Offers      []types.Offer
	Enrollments []types.HostEnrollment
}

// Result reports what a sync did.
type Result struct {
	OffersRevision      string
	CredentialsRevision string
	OffersChanged       []string
	RevokedHosts        []string
	Offers              int
	Credentials         int
}

// Pusher is the broker-side surface a sync needs; the real client
// satisfies it and tests inject a fake.
type Pusher interface {
	PutOffers(ctx context.Context, revision string, offers []brokeradmin.OfferPush) (*brokeradmin.PushResult, error)
	PutCredentials(ctx context.Context, revision string, creds []brokeradmin.CredentialPush) (*brokeradmin.PushResult, error)
}

// Sync pushes offers then credentials.
//
// Order matters: offers first, so a host whose credential has just been
// accepted attaches into a broker that already knows what it might
// serve. The reverse order would leave a runner attached and matching
// nothing for one cycle.
func Sync(ctx context.Context, client Pusher, state State) (Result, error) {
	offers := BuildOffers(state.Offers)
	creds := BuildCredentials(state.Enrollments)
	out := Result{
		OffersRevision:      Revision(offers),
		CredentialsRevision: Revision(creds),
		Offers:              len(offers),
		Credentials:         len(creds),
	}
	offerRes, err := client.PutOffers(ctx, out.OffersRevision, offers)
	if err != nil {
		return out, fmt.Errorf("push offers: %w", err)
	}
	if offerRes != nil {
		out.OffersChanged = offerRes.Changed
	}
	credRes, err := client.PutCredentials(ctx, out.CredentialsRevision, creds)
	if err != nil {
		return out, fmt.Errorf("push credentials: %w", err)
	}
	if credRes != nil {
		out.RevokedHosts = credRes.RevokedHosts
	}
	return out, nil
}

// BuildOffers projects pool offers into the broker's offers[] grammar.
//
// What is NOT here is the point: no backend URL, no transports, no work
// unit, no extractor. Those are runner facts the broker learns at attach
// and freezes; the controller could only ever have been guessing at
// them, and a guess that disagreed with the runner used to be a
// configuration error nobody could see.
func BuildOffers(offers []types.Offer) []brokeradmin.OfferPush {
	out := make([]brokeradmin.OfferPush, 0, len(offers))
	for _, o := range offers {
		if strings.TrimSpace(o.OfferingID) == "" || strings.TrimSpace(o.CapabilityID) == "" {
			continue
		}
		push := brokeradmin.OfferPush{
			OfferingID:  o.OfferingID,
			Capability:  o.CapabilityID,
			Protocol:    o.Protocol,
			Price:       brokeradmin.OfferPushPrice{AmountWei: o.Price.AmountWei, PerUnits: o.Price.PerUnits},
			Extra:       o.Extra,
			Constraints: o.Constraints,
			Match:       MatchFor(o),
			// The pool authors the steps; the broker executes them
			// (decision 6b). A runner may suggest steps and never
			// self-certify.
			Certification: CertificationPolicy(o.CapabilityID, identityValue(o.Extra, "openai", "model")),
			Disabled:      o.Status != types.OfferStatusActive,
		}
		if push.Price.PerUnits == 0 {
			push.Price.PerUnits = 1
		}
		out = append(out, push)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OfferingID < out[j].OfferingID })
	return out
}

// MatchFor derives the offer's runner selector from the workload
// identity the operator already declared in `extra`.
//
// The broker freezes a runner's declared identity INTO the offer's
// extra, so an offer that names a model in extra is exactly an offer
// that wants runners serving that model. Deriving the selector keeps the
// two from disagreeing; an offer that names no identity matches every
// runner declaring its capability, which is what a single-model pool
// wants.
func MatchFor(o types.Offer) map[string]string {
	match := map[string]string{}
	if model := identityValue(o.Extra, "openai", "model"); model != "" {
		match["identity.openai.model"] = model
	}
	if len(match) == 0 {
		return nil
	}
	return match
}

func identityValue(extra map[string]any, group, key string) string {
	if extra == nil {
		return ""
	}
	if nested, ok := extra[group].(map[string]any); ok {
		if v, ok := nested[key].(string); ok {
			return strings.TrimSpace(v)
		}
	}
	// Some callers flatten the dotted form.
	if v, ok := extra[group+"."+key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// BuildCredentials projects host enrollments into the broker's
// credential sync. Only the hash travels: the controller mints the
// secret and hands it to the member, and the broker never needs it.
//
// An enrollment that disappears from this list, or arrives revoked, is a
// revoke on the broker — which deletes the secret and closes that
// host's connections.
func BuildCredentials(enrollments []types.HostEnrollment) []brokeradmin.CredentialPush {
	out := make([]brokeradmin.CredentialPush, 0, len(enrollments))
	for _, e := range enrollments {
		secret := strings.TrimSpace(e.BrokerSessionCredential)
		state := "active"
		if e.Status == types.HostEnrollmentRevoked || e.Status == types.HostEnrollmentRetired || !e.RevokedAt.IsZero() {
			state = "revoked"
		}
		if secret == "" && state != "revoked" {
			// Nothing to authenticate with: pushing an empty hash would
			// be rejected, and silently dropping it would leave the host
			// unable to attach with no explanation. Skip and let the
			// enrollment surface show it has no credential.
			continue
		}
		push := brokeradmin.CredentialPush{
			CredentialID: e.ID,
			// The bundle sets LIVEPEER_HOST_ID to the enrollment id, so
			// the host the broker sees is the host the pool enrolled.
			HostID:           e.ID,
			Kind:             "bearer",
			Label:            e.HostLabel,
			MemberEthAddress: e.MemberEthAddress,
			State:            state,
		}
		if secret != "" {
			sum := sha256.Sum256([]byte(secret))
			push.TokenSHA256 = hex.EncodeToString(sum[:])
		}
		out = append(out, push)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CredentialID < out[j].CredentialID })
	return out
}

// Revision is a content hash of the pushed set. Both pushes are
// idempotent on the broker side, and a stable revision is what makes
// "nothing changed" observable rather than inferred.
func Revision(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "ctl-" + hex.EncodeToString(sum[:])[:16]
}
