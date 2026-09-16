package types

import "time"

// RedemptionInclusion qualifies revenue by the canonical block that included
// the successful redemption, rather than the daemon's observation-time round.
type RedemptionInclusion struct {
	TxHash           []byte    `json:"tx_hash"`
	BlockNumber      uint64    `json:"block_number"`
	BlockHash        []byte    `json:"block_hash"`
	Round            int64     `json:"round"`
	ObservedHead     uint64    `json:"observed_head"`
	ObservedHeadHash []byte    `json:"observed_head_hash"`
	CheckedAt        time.Time `json:"checked_at"`
}

// RevenueCoverage binds a source observation to a fresh canonical chain prefix.
type RevenueCoverage struct {
	Head                 uint64
	HeadHash             []byte
	FinalizedBlock       uint64
	FinalizedBlockHash   []byte
	CompleteThroughRound int64
	ObservedAt           time.Time
}
