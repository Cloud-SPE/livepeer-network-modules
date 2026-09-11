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
// State is what the controller pushes. Offers are already in wire
// shape: they are derived from the enabled template set, not stored,
// so there is nothing gained by carrying the intermediate form.
type State struct {
	Offers      []brokeradmin.OfferPush
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
	offers := state.Offers
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
