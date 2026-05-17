package server

import (
	"math/big"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
)

func mustBig(dec string) *big.Int {
	out, ok := new(big.Int).SetString(dec, 10)
	if !ok {
		return big.NewInt(0)
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func nowUTC() time.Time {
	return time.Now().UTC()
}

func isNearStale(snap health.Snapshot) bool {
	if snap.ProbedAt.IsZero() || snap.StaleAfter.IsZero() {
		return false
	}
	ttl := snap.StaleAfter.Sub(snap.ProbedAt)
	if ttl <= 0 {
		return false
	}
	remaining := snap.StaleAfter.Sub(nowUTC())
	return remaining > 0 && remaining*4 < ttl
}
