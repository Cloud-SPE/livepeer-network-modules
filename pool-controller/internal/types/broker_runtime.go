package types

import "time"

// DesiredBrokerRuntime records the last offer/credential push the
// controller made to its broker (plan 0043 item 17).
//
// It used to hold a rendered broker config file: the controller
// generated the broker's entire host-config, including runner facts it
// could only guess at, and an "apply" wrote that file out and reloaded
// the broker. Runners now tell the broker what they are, so the
// controller pushes only what it owns — the offer set and the
// credentials that may attach — over the admin API, and there is no
// file and no reload.
type DesiredBrokerRuntime struct {
	// Revision is the content hash of the pushed offer set. Identical
	// state pushes an identical revision, which is what makes "nothing
	// changed" observable rather than inferred.
	Revision string `json:"revision"`
	// CredentialsRevision is the same for the credential set; the two
	// move independently.
	CredentialsRevision string    `json:"credentials_revision,omitempty"`
	PushedAt            time.Time `json:"pushed_at"`
	OfferCount          int       `json:"offer_count"`
	CredentialCount     int       `json:"credential_count"`
	MemberCount         int       `json:"member_count"`
	// ChangedOffers and RevokedHosts are what the broker reported the
	// push actually changed.
	ChangedOffers []string `json:"changed_offers,omitempty"`
	RevokedHosts  []string `json:"revoked_hosts,omitempty"`
	// PushError records why the last push failed, so an operator sees a
	// stale broker rather than assuming a successful sync.
	PushError string `json:"push_error,omitempty"`
}
