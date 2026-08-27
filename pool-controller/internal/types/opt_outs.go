package types

import "time"

// MemberTemplateOptOut records that a member does not want one of the
// pool's templates placed on their hardware.
//
// Opting OUT is the only direction a member may move (plan 0044
// decision 2). A member cannot opt in to a higher-demand template,
// because that would let the pool's scarce capacity be claimed by
// whoever asked first rather than by policy; but a member who does not
// want to run, say, image generation on their card is entitled to say
// so, and the placement engine must honour it.
//
// Scope: an opt-out with no HardwareUnitID covers every GPU the member
// has. One naming a unit covers only that card, so a member with two
// cards can decline a workload on one of them.
type MemberTemplateOptOut struct {
	ID               string    `json:"id"`
	MemberEthAddress string    `json:"member_eth_address"`
	TemplateID       string    `json:"template_id"`
	HardwareUnitID   string    `json:"hardware_unit_id,omitempty"`
	Reason           string    `json:"reason,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// Covers reports whether this opt-out applies to a given GPU.
func (o MemberTemplateOptOut) Covers(hardwareUnitID string) bool {
	return o.HardwareUnitID == "" || o.HardwareUnitID == hardwareUnitID
}
