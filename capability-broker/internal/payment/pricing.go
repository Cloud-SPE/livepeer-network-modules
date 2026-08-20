package payment

import "math/big"

// BillFor returns the total wei owed for `units` cumulative work units:
//
//	bill(U) = ceil(U * price / perUnits)
//
// This is the normative rule in
// livepeer-network-protocol/protocols/offering-axes.md §6.1, and the
// payee daemon's ledger computes the identical function. It is
// duplicated rather than shared because the two live in separate
// modules; the spec, not either copy, is the source of truth. Change one
// and you must change the spec and the other.
//
// Ceiling, so a payee is never left short on work already delivered.
// Cumulative, so an increment costs bill(after) - bill(before) and the
// total never depends on how the work was chunked.
//
// perUnits of 0 means 1.
func BillFor(price *big.Int, perUnits uint64, units uint64) *big.Int {
	if price == nil || price.Sign() == 0 || units == 0 {
		return new(big.Int)
	}
	if perUnits == 0 {
		perUnits = 1
	}
	total := new(big.Int).Mul(price, new(big.Int).SetUint64(units))
	quo, rem := new(big.Int).QuoRem(total, new(big.Int).SetUint64(perUnits), new(big.Int))
	if rem.Sign() != 0 {
		quo.Add(quo, big.NewInt(1))
	}
	return quo
}

// RunwayUnits returns how many further work units `balance` covers at
// this price — the inverse of BillFor, floored, because a partially
// affordable unit is not runway.
func RunwayUnits(balance, price *big.Int, perUnits uint64) int64 {
	if balance == nil || price == nil || price.Sign() <= 0 || balance.Sign() <= 0 {
		return 0
	}
	if perUnits == 0 {
		perUnits = 1
	}
	units := new(big.Int).Mul(balance, new(big.Int).SetUint64(perUnits))
	units.Div(units, price)
	if !units.IsInt64() {
		return int64(^uint64(0) >> 1)
	}
	return units.Int64()
}
