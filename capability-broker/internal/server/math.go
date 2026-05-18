package server

import (
	"math/big"
	"time"
)

func mustBig(dec string) *big.Int {
	out, ok := new(big.Int).SetString(dec, 10)
	if !ok {
		return big.NewInt(0)
	}
	return out
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
