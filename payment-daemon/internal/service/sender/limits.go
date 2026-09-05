package sender

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Spend limits.
//
// A gateway signs whatever price the resolver hands it, and the quote is
// where a bad price comes from — so no consistency check can catch one.
// The only defence is the payer's own policy about what it will pay,
// enforced here rather than in gateway code: the daemon holds the keys,
// so one implementation covers every SDK and every caller bug above it.
//
// Two controls, doing different jobs:
//
//   - MaxPaymentWei is a circuit breaker. Unit-agnostic, one number,
//     required in chain mode. It bounds the blast radius of a runaway
//     loop or a fat-fingered funding call, and it means something no
//     matter what the workload is denominated in.
//
//   - MaxPricePerUnit is price policy. Keyed by work-unit name, because
//     that is the denominator a price is quoted in and the manifest
//     declares it before anything is minted. Optional, and diverse
//     workloads need it: a fair price for `tokens` and one for
//     `video_seconds` differ by orders of magnitude, so a single number
//     cannot serve both.
//
// Note what is NOT at risk: a payment cannot be charged beyond its
// funded value, so an overpriced orch delivers less work rather than
// draining more money. These limits guard against waste and runaway, not
// unbounded loss.
type Limits struct {
	// MaxPaymentWei caps funded_value_wei for a single mint. Nil means
	// unlimited, which chain mode refuses at startup.
	MaxPaymentWei *big.Int

	// MaxPricePerUnit maps a work-unit name to the highest price in wei
	// this payer will accept for one unit of it. A unit absent from the
	// map has no rate policy — the circuit breaker still applies.
	MaxPricePerUnit map[string]*big.Int
}

// CheckMint refuses a mint that breaches policy, BEFORE anything is
// signed. Errors name the limit and both numbers, because the operator
// reading the log is the person who has to decide whether the limit is
// wrong or the price is.
func (l Limits) CheckMint(workUnit string, pricePerUnitWei *big.Int, perUnits uint64, fundedValueWei *big.Int) error {
	if l.MaxPaymentWei != nil && fundedValueWei != nil && fundedValueWei.Cmp(l.MaxPaymentWei) > 0 {
		return fmt.Errorf("funded_value %s wei exceeds max-payment-wei %s: "+
			"raise the limit if this spend is intended", fundedValueWei, l.MaxPaymentWei)
	}
	if pricePerUnitWei == nil {
		return nil
	}
	ceiling, ok := l.MaxPricePerUnit[strings.TrimSpace(workUnit)]
	if !ok || ceiling == nil {
		return nil
	}
	// Compare like with like: a price is quoted per `perUnits` units, so
	// the ceiling — which is per single unit — scales by the same
	// denominator before the comparison.
	if perUnits == 0 {
		perUnits = 1
	}
	scaled := new(big.Int).Mul(ceiling, new(big.Int).SetUint64(perUnits))
	if pricePerUnitWei.Cmp(scaled) > 0 {
		return fmt.Errorf("price %s wei per %d %s exceeds max-price-per-unit %s wei per %s",
			pricePerUnitWei, perUnits, workUnit, ceiling, workUnit)
	}
	return nil
}

// ParseMaxPricePerUnit reads the `unit=wei,unit=wei` flag form.
func ParseMaxPricePerUnit(raw string) (map[string]*big.Int, error) {
	out := map[string]*big.Int{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		unit, value, ok := strings.Cut(pair, "=")
		unit = strings.TrimSpace(unit)
		value = strings.TrimSpace(value)
		if !ok || unit == "" || value == "" {
			return nil, fmt.Errorf("malformed entry %q: want unit=wei", pair)
		}
		wei, ok := new(big.Int).SetString(value, 10)
		if !ok || wei.Sign() < 0 {
			return nil, fmt.Errorf("entry %q: %q is not a non-negative decimal integer", pair, value)
		}
		out[unit] = wei
	}
	return out, nil
}

// Describe renders the active policy for the startup log, so an operator
// can see what their daemon will and will not sign.
func (l Limits) Describe() string {
	parts := []string{"max_payment_wei=unlimited"}
	if l.MaxPaymentWei != nil {
		parts[0] = "max_payment_wei=" + l.MaxPaymentWei.String()
	}
	if len(l.MaxPricePerUnit) == 0 {
		parts = append(parts, "max_price_per_unit=(none set — only the circuit breaker applies)")
		return strings.Join(parts, " ")
	}
	units := make([]string, 0, len(l.MaxPricePerUnit))
	for u := range l.MaxPricePerUnit {
		units = append(units, u)
	}
	sort.Strings(units)
	rates := make([]string, 0, len(units))
	for _, u := range units {
		rates = append(rates, u+"="+l.MaxPricePerUnit[u].String())
	}
	parts = append(parts, "max_price_per_unit="+strings.Join(rates, ","))
	return strings.Join(parts, " ")
}
