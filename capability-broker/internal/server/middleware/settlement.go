package middleware

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

func validateExpectedPriceForRequest(paymentBytes []byte, capability, offering string, spec CapabilitySpec) error {
	var pay pb.Payment
	if err := proto.Unmarshal(paymentBytes, &pay); err != nil {
		return fmt.Errorf("payment is malformed: %w", err)
	}
	price := pay.GetExpectedPrice()
	if price == nil {
		return errors.New("payment expected_price is missing")
	}
	if price.GetPricePerUnit() <= 0 {
		return errors.New("payment expected_price.price_per_unit must be > 0")
	}
	if price.GetPixelsPerUnit() <= 0 {
		return errors.New("payment expected_price.pixels_per_unit must be > 0")
	}
	meta, ok := parseExpectedPriceConstraint(price.GetConstraint())
	if !ok {
		return errors.New("payment expected_price.constraint is not parseable")
	}
	if meta.capability != capability {
		return fmt.Errorf("payment capability %q does not match request capability %q", meta.capability, capability)
	}
	if meta.offering != offering {
		return fmt.Errorf("payment offering %q does not match request offering %q", meta.offering, offering)
	}
	if meta.workUnitName != "" && spec.WorkUnit != "" && meta.workUnitName != spec.WorkUnit {
		return fmt.Errorf("payment work_unit %q does not match broker work_unit %q", meta.workUnitName, spec.WorkUnit)
	}
	if spec.PricePerWorkUnitWei != nil {
		if !spec.PricePerWorkUnitWei.IsInt64() {
			return errors.New("broker price_per_work_unit_wei exceeds payment wire int64 range")
		}
		if price.GetPricePerUnit() != spec.PricePerWorkUnitWei.Int64() {
			return fmt.Errorf("payment price_per_unit %d does not match broker price %s", price.GetPricePerUnit(), spec.PricePerWorkUnitWei.String())
		}
	}
	// pixels_per_unit is go-livepeer's name for the price denominator
	// (offering-axes.md §6.3). It must equal the offering's per_units,
	// or payer and payee are pricing the same work differently.
	wantPerUnits := int64(1)
	if spec.PerUnits > 1 {
		wantPerUnits = int64(spec.PerUnits)
	}
	if price.GetPixelsPerUnit() != wantPerUnits {
		return fmt.Errorf("payment pixels_per_unit %d does not match the offering's per_units %d",
			price.GetPixelsPerUnit(), wantPerUnits)
	}
	return nil
}

// ValidateExpectedPriceForRequest validates a ticket envelope used solely to
// fund a wholesale account. It does not authorize a workload.
func ValidateExpectedPriceForRequest(paymentBytes []byte, capability, offering string, spec CapabilitySpec) error {
	return validateExpectedPriceForRequest(paymentBytes, capability, offering, spec)
}

type expectedPriceMeta struct {
	capability            string
	offering              string
	workUnitName          string
	estimatedUnits        uint64
	quoteID               string
	quoteVersion          uint64
	constraintFingerprint []byte
	routeFingerprint      []byte
}

func parseExpectedPriceConstraint(raw string) (expectedPriceMeta, bool) {
	if strings.TrimSpace(raw) == "" {
		return expectedPriceMeta{}, false
	}
	var out expectedPriceMeta
	for _, part := range strings.Split(raw, ";") {
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch key {
		case "cap":
			s, err := url.QueryUnescape(val)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.capability = s
		case "off":
			s, err := url.QueryUnescape(val)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.offering = s
		case "wu":
			s, err := url.QueryUnescape(val)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.workUnitName = s
		case "est":
			n, err := strconv.ParseUint(val, 10, 64)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.estimatedUnits = n
		case "qid":
			s, err := url.QueryUnescape(val)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.quoteID = s
		case "qv":
			n, err := strconv.ParseUint(val, 10, 64)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.quoteVersion = n
		case "cfp":
			b, err := hex.DecodeString(val)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.constraintFingerprint = b
		case "rfp":
			b, err := hex.DecodeString(val)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.routeFingerprint = b
		}
	}
	if out.quoteID == "" || len(out.constraintFingerprint) == 0 || len(out.routeFingerprint) == 0 {
		return expectedPriceMeta{}, false
	}
	return out, true
}
