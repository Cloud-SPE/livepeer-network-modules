package types

import "time"

// ConflictResolution is what an operator decided about a contested GPU.
type ConflictResolution string

const (
	ConflictOpen        ConflictResolution = "open"
	ConflictTransferred ConflictResolution = "transferred"
	ConflictRejected    ConflictResolution = "rejected"
)

// HardwareClaimConflict is a GPU one member holds and another claimed.
//
// The claim is REFUSED, not recorded as ownership: writing the
// challenger's unit would mean anyone could take a member's card
// contested — and stop them earning — just by declaring its UUID. The
// incumbent keeps the GPU and the challenger's claim lands here instead,
// which is what plan 0040 §4.2 means by uniqueness blocking activation:
// the challenger's card does not activate, the incumbent's is untouched.
//
// Only a person can tell the two real causes apart. A member who sold
// their hardware and never retired the enrolment looks identical, on the
// wire, to someone cloning a UUID to farm a second identity — so this
// sits on the operator's queue rather than in policy.
type HardwareClaimConflict struct {
	// ID is derived from the GPU and the challenger, so one dispute is
	// one record however many times the agent re-attaches.
	ID                   string `json:"id"`
	GPUUUID              string `json:"gpu_uuid"`
	ChallengerEthAddress string `json:"challenger_eth_address"`
	ChallengerHostID     string `json:"challenger_host_id,omitempty"`
	IncumbentEthAddress  string `json:"incumbent_eth_address"`
	// Attempts counts how many relays have seen this claim. A member
	// whose agent keeps re-attaching is not a new dispute each time,
	// but the count is worth having: a single mistaken enrolment and a
	// host retrying for a week look different.
	Attempts    int                `json:"attempts"`
	FirstSeenAt time.Time          `json:"first_seen_at"`
	LastSeenAt  time.Time          `json:"last_seen_at"`
	Resolution  ConflictResolution `json:"resolution"`
	ResolvedBy  string             `json:"resolved_by,omitempty"`
	ResolvedAt  time.Time          `json:"resolved_at,omitempty"`
	Reason      string             `json:"reason,omitempty"`
}

// Open reports whether this dispute still needs a person.
func (c HardwareClaimConflict) Open() bool {
	return c.Resolution == "" || c.Resolution == ConflictOpen
}
