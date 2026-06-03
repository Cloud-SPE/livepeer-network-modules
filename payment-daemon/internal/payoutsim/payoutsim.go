package payoutsim

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

type Scenario struct {
	Version      int              `yaml:"version"`
	Name         string           `yaml:"name"`
	ETHUSD       float64          `yaml:"eth_usd"`
	Seed         int64            `yaml:"seed"`
	Simulation   SimulationConfig `yaml:"simulation"`
	PayoutPolicy PayoutPolicy     `yaml:"payout_policy"`
	Offerings    []Offering       `yaml:"offerings"`
}

type SimulationConfig struct {
	Hours  float64 `yaml:"hours"`
	Trials int     `yaml:"trials"`
}

type Offering struct {
	Name         string            `yaml:"name"`
	CapabilityID string            `yaml:"capability_id"`
	OfferingID   string            `yaml:"offering_id"`
	Interaction  string            `yaml:"interaction"`
	WorkUnit     string            `yaml:"work_unit"`
	Price        Price             `yaml:"price"`
	Payout       Payout            `yaml:"payout"`
	Workload     Workload          `yaml:"workload"`
	Gateway      GatewayPolicy     `yaml:"gateway"`
	Hardware     []HardwareProfile `yaml:"hardware"`
}

type Price struct {
	AmountWei string `yaml:"amount_wei"`
	PerUnits  uint64 `yaml:"per_units"`
}

type Payout struct {
	FaceValueWei string `yaml:"face_value_wei"`
}

type PayoutPolicy struct {
	TargetFaceValueWei    string  `yaml:"target_face_value_wei"`
	SenderMaxFaceValueWei string  `yaml:"sender_max_face_value_wei"`
	GasPriceWei           string  `yaml:"gas_price_wei"`
	RedeemGas             uint64  `yaml:"redeem_gas"`
	TargetHoursPerWin     float64 `yaml:"target_hours_per_win"`
}

type Workload struct {
	UnitsPerHour            float64  `yaml:"units_per_hour"`
	RequestsPerHour         float64  `yaml:"requests_per_hour"`
	UnitsPerRequest         float64  `yaml:"units_per_request"`
	TokenMix                TokenMix `yaml:"token_mix"`
	TargetRevenuePerHourUSD float64  `yaml:"target_revenue_per_hour_usd"`
}

type TokenMix struct {
	InputTokensPerDay  float64 `yaml:"input_tokens_per_day"`
	OutputTokensPerDay float64 `yaml:"output_tokens_per_day"`
	OutputWeight       float64 `yaml:"output_weight"`
}

func (m TokenMix) configured() bool {
	return m.InputTokensPerDay > 0 || m.OutputTokensPerDay > 0
}

type GatewayPolicy struct {
	EstimatedUnitsMultiplier  float64 `yaml:"estimated_units_multiplier"`
	LiveTopupThresholdSeconds int     `yaml:"live_topup_threshold_seconds"`
	LiveTopupFundSeconds      int     `yaml:"live_topup_fund_seconds"`
}

type HardwareProfile struct {
	Name          string  `yaml:"name"`
	UnitsPerHour  float64 `yaml:"units_per_hour"`
	HourlyCostUSD float64 `yaml:"hourly_cost_usd"`
}

type Report struct {
	Scenario  Scenario
	Offerings []OfferingReport
}

type OfferingReport struct {
	Offering                        Offering
	FaceValueWei                    *big.Int
	PricePerUnitWei                 *big.Rat
	BillableUnitsPerHour            float64
	BillableUnitsPerRequest         float64
	TokenMix                        *TokenMixReport
	ExpectedValuePerRequestWei      *big.Rat
	ExpectedValuePerTicketWei       *big.Rat
	ConfiguredRevenuePerHourWei     *big.Rat
	ConfiguredRevenuePerHourUSD     float64
	TargetPriceFloorWeiPerUnit      *big.Rat
	TargetPriceFloorWholeWeiPerUnit *big.Int
	FairnessRatio                   float64
	WinOddsPerTicket                float64
	WinProbabilityPerTicket         float64
	TicketsPerRequest               int
	ExpectedWinsPerHour             float64
	ExpectedHoursPerWin             float64
	TargetHoursPerWin               float64
	RequiredRevenuePerHourWei       *big.Rat
	RequiredRevenuePerHourUSD       float64
	RequiredUnitsPerHourAtPrice     float64
	VolumeMultiplierForTarget       float64
	ExpectedRequestsPerWin          float64
	ExpectedUnitsPerWin             float64
	GatewayEstimatedUnits           uint64
	GatewayFundedValueWei           *big.Int
	GatewayFundingToEVRatio         float64
	GatewayLiveTopupThresholdUnits  uint64
	GatewayLiveTopupFundUnits       uint64
	RedemptionCostWei               *big.Int
	RedemptionMargin                float64
	SenderMaxFaceValueWei           *big.Int
	MonteCarlo                      MonteCarloReport
	Hardware                        []HardwareReport
	HostConfigSnippet               string
	Warnings                        []string
}

type TokenMixReport struct {
	InputTokensPerDay     float64
	OutputTokensPerDay    float64
	OutputWeight          float64
	BillableTokensPerDay  float64
	BillableTokensPerHour float64
}

type HardwareReport struct {
	Name                  string
	UnitsPerHour          float64
	HourlyCostUSD         float64
	RevenuePerHourUSD     float64
	ProfitPerHourUSD      float64
	ExpectedPayout1DayUSD float64
	ExpectedPayout7DayUSD float64
	Cost1DayUSD           float64
	Cost7DayUSD           float64
	Profit1DayUSD         float64
	Profit7DayUSD         float64
	ExpectedWins1Day      float64
	ExpectedWins7Day      float64
	ExpectedHoursPerWin   float64
	BreakEvenUnitsPerHour float64
}

type MonteCarloReport struct {
	Trials         int
	Hours          float64
	MeanWins       float64
	MeanRevenueWei *big.Int
	P50HoursPerWin float64
	P90HoursPerWin float64
	P99HoursPerWin float64
}

func LoadScenario(path string) (Scenario, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, err
	}
	var sc Scenario
	if err := yaml.Unmarshal(raw, &sc); err != nil {
		return Scenario{}, fmt.Errorf("decode yaml: %w", err)
	}
	if err := validateScenario(sc); err != nil {
		return Scenario{}, err
	}
	return sc, nil
}

func Generate(sc Scenario) (Report, error) {
	if err := validateScenario(sc); err != nil {
		return Report{}, err
	}
	out := Report{
		Scenario:  sc,
		Offerings: make([]OfferingReport, 0, len(sc.Offerings)),
	}
	baseSeed := sc.Seed
	if baseSeed == 0 {
		baseSeed = 1
	}
	for i, off := range sc.Offerings {
		rep, err := analyzeOffering(off, sc.PayoutPolicy, sc.ETHUSD, sc.Simulation, baseSeed+int64(i))
		if err != nil {
			return Report{}, fmt.Errorf("%s: %w", off.Name, err)
		}
		out.Offerings = append(out.Offerings, rep)
	}
	return out, nil
}

func validateScenario(sc Scenario) error {
	if sc.Version == 0 {
		sc.Version = 1
	}
	if sc.ETHUSD <= 0 {
		sc.ETHUSD = 3500
	}
	if len(sc.Offerings) == 0 {
		return errors.New("scenario must include at least one offering")
	}
	if sc.Simulation.Hours <= 0 {
		return errors.New("simulation.hours must be > 0")
	}
	if sc.Simulation.Trials <= 0 {
		return errors.New("simulation.trials must be > 0")
	}
	if sc.PayoutPolicy.TargetFaceValueWei != "" {
		if _, ok := new(big.Int).SetString(sc.PayoutPolicy.TargetFaceValueWei, 10); !ok {
			return fmt.Errorf("invalid payout_policy.target_face_value_wei %q", sc.PayoutPolicy.TargetFaceValueWei)
		}
	}
	if sc.PayoutPolicy.SenderMaxFaceValueWei != "" {
		if _, ok := new(big.Int).SetString(sc.PayoutPolicy.SenderMaxFaceValueWei, 10); !ok {
			return fmt.Errorf("invalid payout_policy.sender_max_face_value_wei %q", sc.PayoutPolicy.SenderMaxFaceValueWei)
		}
	}
	if sc.PayoutPolicy.GasPriceWei != "" {
		if _, ok := new(big.Int).SetString(sc.PayoutPolicy.GasPriceWei, 10); !ok {
			return fmt.Errorf("invalid payout_policy.gas_price_wei %q", sc.PayoutPolicy.GasPriceWei)
		}
	}
	for _, off := range sc.Offerings {
		if off.Name == "" {
			return errors.New("offering name is required")
		}
		if off.CapabilityID == "" || off.OfferingID == "" {
			return fmt.Errorf("%s: capability_id and offering_id are required", off.Name)
		}
		if off.WorkUnit == "" {
			return fmt.Errorf("%s: work_unit is required", off.Name)
		}
		if off.Price.PerUnits == 0 {
			return fmt.Errorf("%s: price.per_units must be > 0", off.Name)
		}
		if _, ok := new(big.Int).SetString(off.Price.AmountWei, 10); !ok {
			return fmt.Errorf("%s: invalid price.amount_wei %q", off.Name, off.Price.AmountWei)
		}
		if off.Payout.FaceValueWei == "" && sc.PayoutPolicy.TargetFaceValueWei == "" {
			return fmt.Errorf("%s: payout.face_value_wei or payout_policy.target_face_value_wei is required", off.Name)
		}
		if off.Payout.FaceValueWei != "" {
			if _, ok := new(big.Int).SetString(off.Payout.FaceValueWei, 10); !ok {
				return fmt.Errorf("%s: invalid payout.face_value_wei %q", off.Name, off.Payout.FaceValueWei)
			}
		}
		if off.Workload.RequestsPerHour <= 0 {
			return fmt.Errorf("%s: workload.requests_per_hour must be > 0", off.Name)
		}
		if off.Workload.TokenMix.configured() {
			if off.Workload.TokenMix.InputTokensPerDay < 0 {
				return fmt.Errorf("%s: workload.token_mix.input_tokens_per_day must be >= 0", off.Name)
			}
			if off.Workload.TokenMix.OutputTokensPerDay < 0 {
				return fmt.Errorf("%s: workload.token_mix.output_tokens_per_day must be >= 0", off.Name)
			}
			if off.Workload.TokenMix.OutputWeight < 0 {
				return fmt.Errorf("%s: workload.token_mix.output_weight must be >= 0", off.Name)
			}
		} else {
			if off.Workload.UnitsPerHour <= 0 {
				return fmt.Errorf("%s: workload.units_per_hour must be > 0", off.Name)
			}
			if off.Workload.UnitsPerRequest <= 0 {
				return fmt.Errorf("%s: workload.units_per_request must be > 0", off.Name)
			}
		}
		if off.Workload.TargetRevenuePerHourUSD <= 0 {
			return fmt.Errorf("%s: workload.target_revenue_per_hour_usd must be > 0", off.Name)
		}
		if off.Gateway.EstimatedUnitsMultiplier <= 0 {
			return fmt.Errorf("%s: gateway.estimated_units_multiplier must be > 0", off.Name)
		}
		for _, hw := range off.Hardware {
			if hw.Name == "" {
				return fmt.Errorf("%s: hardware.name is required", off.Name)
			}
			if hw.UnitsPerHour <= 0 {
				return fmt.Errorf("%s/%s: hardware.units_per_hour must be > 0", off.Name, hw.Name)
			}
			if hw.HourlyCostUSD < 0 {
				return fmt.Errorf("%s/%s: hardware.hourly_cost_usd must be >= 0", off.Name, hw.Name)
			}
		}
	}
	return nil
}

func analyzeOffering(off Offering, policy PayoutPolicy, ethUSD float64, sim SimulationConfig, seed int64) (OfferingReport, error) {
	priceAmount, _ := new(big.Int).SetString(off.Price.AmountWei, 10)
	faceValue, err := resolveFaceValue(off, policy)
	if err != nil {
		return OfferingReport{}, err
	}
	unitsPerHour, unitsPerRequest, tokenMix := effectiveWorkload(off.Workload)
	pricePerUnit := new(big.Rat).SetFrac(priceAmount, new(big.Int).SetUint64(off.Price.PerUnits))
	configuredRevenuePerHourWei := new(big.Rat).Mul(pricePerUnit, new(big.Rat).SetFloat64(unitsPerHour))
	priceFloor := targetPriceFloor(off.Workload.TargetRevenuePerHourUSD, unitsPerHour, ethUSD)
	priceFloorWhole := ceilRat(priceFloor)
	fairnessRatio := ratToFloat64(new(big.Rat).Quo(pricePerUnit, priceFloor))

	evPerRequest := new(big.Rat).Mul(pricePerUnit, new(big.Rat).SetFloat64(unitsPerRequest))
	faceValueRat := new(big.Rat).SetInt(faceValue)
	ticketsPerRequest := 1
	if evPerRequest.Cmp(faceValueRat) > 0 {
		ratio := new(big.Rat).Quo(evPerRequest, faceValueRat)
		ticketsPerRequest = int(math.Ceil(ratToFloat64(ratio)))
	}
	evPerTicket := new(big.Rat).Quo(evPerRequest, new(big.Rat).SetInt64(int64(ticketsPerRequest)))
	winProb := ratToFloat64(new(big.Rat).Quo(evPerTicket, faceValueRat))

	warnings := []string{}
	if winProb <= 0 {
		return OfferingReport{}, fmt.Errorf("derived win probability must be > 0")
	}
	if winProb > 1 {
		warnings = append(warnings, "derived win probability exceeds 1.0; increase face_value_wei or split requests further")
		winProb = 1
	}
	if fairnessRatio < 1 {
		warnings = append(warnings, "configured price is below the target hourly revenue floor")
	}

	expectedWinsPerHour := ratToFloat64(new(big.Rat).Quo(configuredRevenuePerHourWei, faceValueRat))
	expectedHoursPerWin := 0.0
	if expectedWinsPerHour > 0 {
		expectedHoursPerWin = 1 / expectedWinsPerHour
	}
	targetHoursPerWin := policy.TargetHoursPerWin
	if targetHoursPerWin <= 0 {
		targetHoursPerWin = 24
	}
	requiredRevenuePerHourWei := new(big.Rat).Quo(faceValueRat, new(big.Rat).SetFloat64(targetHoursPerWin))
	requiredRevenuePerHourUSD := weiPerHourToUSD(requiredRevenuePerHourWei, ethUSD)
	requiredUnitsPerHourAtPrice := ratToFloat64(new(big.Rat).Quo(requiredRevenuePerHourWei, pricePerUnit))
	volumeMultiplierForTarget := 0.0
	if configuredRevenuePerHourWei.Sign() > 0 {
		volumeMultiplierForTarget = ratToFloat64(new(big.Rat).Quo(requiredRevenuePerHourWei, configuredRevenuePerHourWei))
	}
	if volumeMultiplierForTarget > 10 {
		warnings = append(warnings, "configured workload is far below target payout cadence; aggregate more work, raise price, lower redeemable face value, or use a workload-specific payment lane")
	}
	expectedReqPerWin := 0.0
	if off.Workload.RequestsPerHour > 0 {
		expectedReqPerWin = expectedHoursPerWin * off.Workload.RequestsPerHour
	}
	expectedUnitsPerWin := 0.0
	if ratToFloat64(pricePerUnit) > 0 {
		expectedUnitsPerWin = ratToFloat64(new(big.Rat).Quo(faceValueRat, pricePerUnit))
	}

	estimatedUnits := uint64(math.Ceil(unitsPerRequest * off.Gateway.EstimatedUnitsMultiplier))
	fundedValueWei := fundedValue(priceAmount, off.Price.PerUnits, estimatedUnits)
	gatewayFundingToEVRatio := ratToFloat64(new(big.Rat).Quo(new(big.Rat).SetInt(fundedValueWei), evPerRequest))
	if gatewayFundingToEVRatio < 1 {
		warnings = append(warnings, "gateway funded value is below expected value for the request; sender should reject or top up before signing")
	}

	redemptionCost, hasRedemptionCost := redemptionCost(policy)
	redemptionMargin := 0.0
	if hasRedemptionCost && redemptionCost.Sign() > 0 {
		redemptionMargin = ratToFloat64(new(big.Rat).Quo(faceValueRat, new(big.Rat).SetInt(redemptionCost)))
		if redemptionMargin < 1 {
			warnings = append(warnings, "face value is below estimated redemption cost")
		} else if redemptionMargin < 10 {
			warnings = append(warnings, "face value has less than 10x estimated redemption-cost margin")
		}
	}
	senderMaxFaceValue := optionalInt(policy.SenderMaxFaceValueWei)
	if senderMaxFaceValue != nil && faceValue.Cmp(senderMaxFaceValue) > 0 {
		warnings = append(warnings, "face value exceeds sender max face value; sender should reject these ticket params")
	}

	topupThresholdUnits := uint64(0)
	topupFundUnits := uint64(0)
	if off.Gateway.LiveTopupThresholdSeconds > 0 {
		topupThresholdUnits = uint64(math.Ceil((unitsPerHour / 3600) * float64(off.Gateway.LiveTopupThresholdSeconds)))
	}
	if off.Gateway.LiveTopupFundSeconds > 0 {
		topupFundUnits = uint64(math.Ceil((unitsPerHour / 3600) * float64(off.Gateway.LiveTopupFundSeconds)))
	}

	mc := runMonteCarlo(off, sim, faceValue, pricePerUnit, ticketsPerRequest, winProb, seed)
	hardware := analyzeHardware(off.Hardware, pricePerUnit, faceValueRat, ethUSD)
	snippet := hostConfigSnippet(off, priceFloorWhole)

	return OfferingReport{
		Offering:                        off,
		FaceValueWei:                    faceValue,
		PricePerUnitWei:                 pricePerUnit,
		BillableUnitsPerHour:            unitsPerHour,
		BillableUnitsPerRequest:         unitsPerRequest,
		TokenMix:                        tokenMix,
		ExpectedValuePerRequestWei:      evPerRequest,
		ExpectedValuePerTicketWei:       evPerTicket,
		ConfiguredRevenuePerHourWei:     configuredRevenuePerHourWei,
		ConfiguredRevenuePerHourUSD:     weiPerHourToUSD(configuredRevenuePerHourWei, ethUSD),
		TargetPriceFloorWeiPerUnit:      priceFloor,
		TargetPriceFloorWholeWeiPerUnit: priceFloorWhole,
		FairnessRatio:                   fairnessRatio,
		WinOddsPerTicket:                1 / winProb,
		WinProbabilityPerTicket:         winProb,
		TicketsPerRequest:               ticketsPerRequest,
		ExpectedWinsPerHour:             expectedWinsPerHour,
		ExpectedHoursPerWin:             expectedHoursPerWin,
		TargetHoursPerWin:               targetHoursPerWin,
		RequiredRevenuePerHourWei:       requiredRevenuePerHourWei,
		RequiredRevenuePerHourUSD:       requiredRevenuePerHourUSD,
		RequiredUnitsPerHourAtPrice:     requiredUnitsPerHourAtPrice,
		VolumeMultiplierForTarget:       volumeMultiplierForTarget,
		ExpectedRequestsPerWin:          expectedReqPerWin,
		ExpectedUnitsPerWin:             expectedUnitsPerWin,
		GatewayEstimatedUnits:           estimatedUnits,
		GatewayFundedValueWei:           fundedValueWei,
		GatewayFundingToEVRatio:         gatewayFundingToEVRatio,
		GatewayLiveTopupThresholdUnits:  topupThresholdUnits,
		GatewayLiveTopupFundUnits:       topupFundUnits,
		RedemptionCostWei:               redemptionCost,
		RedemptionMargin:                redemptionMargin,
		SenderMaxFaceValueWei:           senderMaxFaceValue,
		MonteCarlo:                      mc,
		Hardware:                        hardware,
		HostConfigSnippet:               snippet,
		Warnings:                        warnings,
	}, nil
}

func analyzeHardware(profiles []HardwareProfile, pricePerUnit *big.Rat, faceValue *big.Rat, ethUSD float64) []HardwareReport {
	if len(profiles) == 0 {
		return nil
	}
	pricePerUnitUSD := weiToUSD(pricePerUnit, ethUSD)
	out := make([]HardwareReport, 0, len(profiles))
	for _, hw := range profiles {
		revenuePerHourUSD := pricePerUnitUSD * hw.UnitsPerHour
		profitPerHourUSD := revenuePerHourUSD - hw.HourlyCostUSD
		expectedWinsPerHour := 0.0
		revenuePerHourWei := new(big.Rat).Mul(pricePerUnit, new(big.Rat).SetFloat64(hw.UnitsPerHour))
		if faceValue.Sign() > 0 {
			expectedWinsPerHour = ratToFloat64(new(big.Rat).Quo(revenuePerHourWei, faceValue))
		}
		expectedHoursPerWin := 0.0
		if expectedWinsPerHour > 0 {
			expectedHoursPerWin = 1 / expectedWinsPerHour
		}
		breakEvenUnitsPerHour := 0.0
		if pricePerUnitUSD > 0 {
			breakEvenUnitsPerHour = hw.HourlyCostUSD / pricePerUnitUSD
		}
		out = append(out, HardwareReport{
			Name:                  hw.Name,
			UnitsPerHour:          hw.UnitsPerHour,
			HourlyCostUSD:         hw.HourlyCostUSD,
			RevenuePerHourUSD:     revenuePerHourUSD,
			ProfitPerHourUSD:      profitPerHourUSD,
			ExpectedPayout1DayUSD: revenuePerHourUSD * 24,
			ExpectedPayout7DayUSD: revenuePerHourUSD * 24 * 7,
			Cost1DayUSD:           hw.HourlyCostUSD * 24,
			Cost7DayUSD:           hw.HourlyCostUSD * 24 * 7,
			Profit1DayUSD:         profitPerHourUSD * 24,
			Profit7DayUSD:         profitPerHourUSD * 24 * 7,
			ExpectedWins1Day:      expectedWinsPerHour * 24,
			ExpectedWins7Day:      expectedWinsPerHour * 24 * 7,
			ExpectedHoursPerWin:   expectedHoursPerWin,
			BreakEvenUnitsPerHour: breakEvenUnitsPerHour,
		})
	}
	return out
}

func effectiveWorkload(w Workload) (float64, float64, *TokenMixReport) {
	if !w.TokenMix.configured() {
		return w.UnitsPerHour, w.UnitsPerRequest, nil
	}
	outputWeight := w.TokenMix.OutputWeight
	if outputWeight <= 0 {
		outputWeight = 1
	}
	billablePerDay := w.TokenMix.InputTokensPerDay + (w.TokenMix.OutputTokensPerDay * outputWeight)
	billablePerHour := billablePerDay / 24
	billablePerRequest := 0.0
	if w.RequestsPerHour > 0 {
		billablePerRequest = billablePerHour / w.RequestsPerHour
	}
	return billablePerHour, billablePerRequest, &TokenMixReport{
		InputTokensPerDay:     w.TokenMix.InputTokensPerDay,
		OutputTokensPerDay:    w.TokenMix.OutputTokensPerDay,
		OutputWeight:          outputWeight,
		BillableTokensPerDay:  billablePerDay,
		BillableTokensPerHour: billablePerHour,
	}
}

func resolveFaceValue(off Offering, policy PayoutPolicy) (*big.Int, error) {
	raw := off.Payout.FaceValueWei
	if raw == "" {
		raw = policy.TargetFaceValueWei
	}
	if raw == "" {
		return nil, errors.New("face value is required")
	}
	v, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return nil, fmt.Errorf("invalid face value %q", raw)
	}
	return v, nil
}

func redemptionCost(policy PayoutPolicy) (*big.Int, bool) {
	if policy.GasPriceWei == "" || policy.RedeemGas == 0 {
		return nil, false
	}
	gasPrice := optionalInt(policy.GasPriceWei)
	if gasPrice == nil {
		return nil, false
	}
	return new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(policy.RedeemGas)), true
}

func optionalInt(raw string) *big.Int {
	if raw == "" {
		return nil
	}
	v, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return nil
	}
	return v
}

func targetPriceFloor(targetRevenueUSD, unitsPerHour, ethUSD float64) *big.Rat {
	usdTimesWei := new(big.Rat).Mul(new(big.Rat).SetFloat64(targetRevenueUSD), new(big.Rat).SetInt(big.NewInt(1_000_000_000_000_000_000)))
	denom := new(big.Rat).Mul(new(big.Rat).SetFloat64(unitsPerHour), new(big.Rat).SetFloat64(ethUSD))
	return new(big.Rat).Quo(usdTimesWei, denom)
}

func fundedValue(amountWei *big.Int, perUnits uint64, estimatedUnits uint64) *big.Int {
	n := new(big.Int).SetUint64(estimatedUnits)
	d := new(big.Int).SetUint64(perUnits)
	q := new(big.Int).Quo(n, d)
	r := new(big.Int).Mod(n, d)
	if r.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	return q.Mul(q, amountWei)
}

func weiPerHourToUSD(weiPerHour *big.Rat, ethUSD float64) float64 {
	return weiToUSD(weiPerHour, ethUSD)
}

func weiToUSD(wei *big.Rat, ethUSD float64) float64 {
	eth := new(big.Rat).Quo(wei, new(big.Rat).SetInt(big.NewInt(1_000_000_000_000_000_000)))
	usd := new(big.Rat).Mul(eth, new(big.Rat).SetFloat64(ethUSD))
	return ratToFloat64(usd)
}

func runMonteCarlo(off Offering, sim SimulationConfig, faceValue *big.Int, pricePerUnit *big.Rat, ticketsPerRequest int, winProb float64, seed int64) MonteCarloReport {
	rng := rand.New(rand.NewSource(seed))
	requests := int(math.Round(off.Workload.RequestsPerHour * sim.Hours))
	if requests < 1 {
		requests = 1
	}
	winIntervals := make([]float64, 0, sim.Trials)
	totalWins := 0.0
	totalRevenue := new(big.Int)

	for trial := 0; trial < sim.Trials; trial++ {
		lastWinReq := -1
		wins := 0
		revenue := new(big.Int)
		for reqIdx := 0; reqIdx < requests; reqIdx++ {
			for ticketIdx := 0; ticketIdx < ticketsPerRequest; ticketIdx++ {
				if rng.Float64() < winProb {
					wins++
					revenue.Add(revenue, faceValue)
					if lastWinReq >= 0 {
						reqDelta := reqIdx - lastWinReq
						hoursDelta := float64(reqDelta) / off.Workload.RequestsPerHour
						winIntervals = append(winIntervals, hoursDelta)
					}
					lastWinReq = reqIdx
				}
			}
		}
		totalWins += float64(wins)
		totalRevenue.Add(totalRevenue, revenue)
	}

	sort.Float64s(winIntervals)
	return MonteCarloReport{
		Trials:         sim.Trials,
		Hours:          sim.Hours,
		MeanWins:       totalWins / float64(sim.Trials),
		MeanRevenueWei: new(big.Int).Quo(totalRevenue, big.NewInt(int64(sim.Trials))),
		P50HoursPerWin: percentile(winIntervals, 0.50),
		P90HoursPerWin: percentile(winIntervals, 0.90),
		P99HoursPerWin: percentile(winIntervals, 0.99),
	}
}

func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	idx := int(math.Ceil(float64(len(xs))*p)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(xs) {
		idx = len(xs) - 1
	}
	return xs[idx]
}

func hostConfigSnippet(off Offering, floorWei *big.Int) string {
	priceAmount, _ := new(big.Int).SetString(off.Price.AmountWei, 10)
	return fmt.Sprintf(`- id: %q
  offering_id: %q
  interaction_mode: %q
  work_unit:
    name: %q
    extractor: { type: "TODO" }
  price:
    amount_wei: %q
    per_units: %d
  # target floor at current assumptions: %s wei/%s
`, off.CapabilityID, off.OfferingID, off.Interaction, off.WorkUnit, priceAmount.String(), off.Price.PerUnits, floorWei.String(), off.WorkUnit)
}

func ceilRat(r *big.Rat) *big.Int {
	n := new(big.Int).Set(r.Num())
	d := new(big.Int).Set(r.Denom())
	q := new(big.Int).Quo(n, d)
	rem := new(big.Int).Mod(n, d)
	if rem.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

func ratToFloat64(r *big.Rat) float64 {
	f, _ := r.Float64()
	return f
}
