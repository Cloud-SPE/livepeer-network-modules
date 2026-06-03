// Package types — unit conversions for operator-facing inputs.
//
// Two families:
//
//   - percent ↔ ppm: Livepeer stores reward cut / fee share in
//     parts-per-million (1_000_000 = 100%). Operators enter percentages.
//   - decimal ↔ wei: LPT / ETH amounts are stored on-chain in wei (18
//     decimals). Operators enter human decimals ("1", "0.03").
//
// Both conversions are EXACT using math/big rationals and TRUNCATE toward
// zero on excess precision — matching go-livepeer's eth.FromPerc (which
// floors via big.Float) so operators never get a value finer than they can
// express on-chain. No floating point is used, so there is no rounding
// drift.
package types

import (
	"fmt"
	"math/big"
	"strings"
)

// PPMDenominator is the parts-per-million scale: 1_000_000 == 100%.
// Matches Livepeer's MathUtils.PERC_DIVISOR.
const PPMDenominator = 1_000_000

// EthDecimals is the wei scale for both ETH and LPT (both are 18-decimal).
const EthDecimals = 18

var (
	bigTen      = big.NewInt(10)
	ppmDenomRat = new(big.Rat).SetInt64(PPMDenominator)
	hundredRat  = new(big.Rat).SetInt64(100)
)

// PercentToPPM converts an operator-entered percentage string (e.g. "10",
// "95.5") to parts-per-million in [0, 1_000_000]. Excess precision finer
// than 1 ppm (0.0001%) is truncated toward zero, matching livepeer_cli.
// Rejects values outside [0, 100] and unparseable input.
func PercentToPPM(percent string) (uint64, error) {
	r, err := parseNonNegativeDecimal(percent)
	if err != nil {
		return 0, fmt.Errorf("percent: %w", err)
	}
	if r.Cmp(hundredRat) > 0 {
		return 0, fmt.Errorf("percent: %s exceeds 100", strings.TrimSpace(percent))
	}
	// ppm = percent * 1_000_000 / 100, floored (truncate toward zero; value
	// is non-negative so floor == truncate).
	scaled := new(big.Rat).Mul(r, ppmDenomRat)
	scaled.Quo(scaled, hundredRat)
	ppm := floorRat(scaled)
	return ppm.Uint64(), nil
}

// PPMToPercent renders a ppm value back to a percentage string for display.
// Trailing zeros are trimmed ("100", "95.5", "33.3333").
func PPMToPercent(ppm uint64) string {
	r := new(big.Rat).SetFrac(
		new(big.Int).Mul(new(big.Int).SetUint64(ppm), big.NewInt(100)),
		big.NewInt(PPMDenominator),
	)
	return trimDecimal(r.FloatString(4))
}

// DecimalToWei converts an operator-entered decimal amount (e.g. "1",
// "0.03") to wei using the given number of decimals. Sub-wei precision is
// truncated toward zero. Rejects negative or unparseable input.
func DecimalToWei(amount string, decimals int) (*big.Int, error) {
	r, err := parseNonNegativeDecimal(amount)
	if err != nil {
		return nil, fmt.Errorf("amount: %w", err)
	}
	scale := new(big.Int).Exp(bigTen, big.NewInt(int64(decimals)), nil)
	r.Mul(r, new(big.Rat).SetInt(scale))
	return floorRat(r), nil
}

// WeiToDecimal renders a wei amount back to a decimal string with the given
// number of decimals, trailing zeros trimmed ("1", "0.03").
func WeiToDecimal(wei *big.Int, decimals int) string {
	if wei == nil {
		return "0"
	}
	scale := new(big.Int).Exp(bigTen, big.NewInt(int64(decimals)), nil)
	r := new(big.Rat).SetFrac(wei, scale)
	return trimDecimal(r.FloatString(decimals))
}

// parseNonNegativeDecimal parses a base-10 decimal string into a big.Rat,
// rejecting blanks, signs, scientific notation, and negatives. We restrict
// the grammar deliberately so "1e3" / "0x.." / "-1" never silently parse.
func parseNonNegativeDecimal(s string) (*big.Rat, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return nil, fmt.Errorf("empty value")
	}
	for _, c := range t {
		if (c < '0' || c > '9') && c != '.' {
			return nil, fmt.Errorf("%q is not a non-negative decimal", s)
		}
	}
	if strings.Count(t, ".") > 1 {
		return nil, fmt.Errorf("%q has more than one decimal point", s)
	}
	r, ok := new(big.Rat).SetString(t)
	if !ok {
		return nil, fmt.Errorf("%q is not a valid number", s)
	}
	return r, nil
}

// floorRat returns floor(r) as a big.Int. For non-negative r this equals
// truncation toward zero.
func floorRat(r *big.Rat) *big.Int {
	q := new(big.Int)
	q.Quo(r.Num(), r.Denom()) // Quo truncates toward zero; r >= 0 here.
	return q
}

// trimDecimal removes trailing zeros (and a dangling decimal point) from a
// fixed-precision decimal string.
func trimDecimal(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		return "0"
	}
	return s
}
