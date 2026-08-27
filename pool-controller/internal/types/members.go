package types

// MemberStatus is the pool's verdict on a member as a whole, separate
// from the state of anything they run. It survived the legacy member
// model because PoolMember carries it (connected_pool.go) and existing
// pool_members_v2 rows already persist these strings.
type MemberStatus string

const (
	MemberStatusActive    MemberStatus = "active"
	MemberStatusSuspended MemberStatus = "suspended"
)
