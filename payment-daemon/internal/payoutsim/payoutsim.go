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
	Version    int              `yaml:"version"`
	Name       string           `yaml:"name"`
	ETHUSD     float64          `yaml:"eth_usd"`
	Seed       int64            `yaml:"seed"`
	Simulation SimulationConfig `yaml:"simulation"`
	Offerings  []Offering       `yaml:"offerings"`
}

type SimulationConfig struct {
	Hours  float64 `yaml:"hours"`
	Trials int     `yaml:"trials"`
}

type Offering struct {
	Name         string          `yaml:"name"`
	CapabilityID string          `yaml:"capability_id"`
	OfferingID   string          `yaml:"offering_id"`
	Interaction  string          `yaml:"interaction"`
	WorkUnit     string          `yaml:"work_unit"`
	Price        Price           `yaml:"price"`
	Payout       Payout          `yaml:"payout"`
	Workload     Workload        `yaml:"workload"`
	Gateway      GatewayPolicy   `yaml:"gateway"`
}

type Price struct {
	AmountWei string `yaml:"amount_wei"`
	PerUnits  uint64 `yaml:"per_units"`
}

type Payout struct {
	FaceValueWei string `yaml:"face_value_wei"`
}

type Workload struct {
	UnitsPerHour              float64 `yaml:"units_per_hour"`
	RequestsPerHour           float64 `yaml:"requests_per_hour"`
	UnitsPerRequest           float64 `yaml:"units_per_request"`
	TargetRevenuePerHourUSD   float64 `yaml:"target_revenue_per_hour_usd"`
}

type GatewayPolicy struct {
	EstimatedUnitsMultiplier  float64 `yaml:"estimated_units_multiplier"`
	LiveTopupThresholdSeconds int     `yaml:"live_topup_threshold_seconds"`
	LiveTopupFundSeconds      int     `yaml:"live_topup_fund_seconds"`
}

type Report struct {
	Scenario  Scenario
	Offerings []OfferingReport
}

type OfferingReport struct {
	Offering                         Offering
	PricePerUnitWei                 *big.Rat
	ConfiguredRevenuePerHourWei     *big.Rat
	ConfiguredRevenuePerHourUSD     float64
	TargetPriceFloorWeiPerUnit      *big.Rat
	TargetPriceFloorWholeWeiPerUnit *big.Int
	FairnessRatio                   float64
	WinProbabilityPerTicket         float64
	TicketsPerRequest               int
	ExpectedWinsPerHour             float64
	ExpectedHoursPerWin             float64
	ExpectedRequestsPerWin          float64
	ExpectedUnitsPerWin             float64
	GatewayEstimatedUnits           uint64
	GatewayFundedValueWei           *big.Int
	GatewayLiveTopupThresholdUnits  uint64
	GatewayLiveTopupFundUnits       uint64
	MonteCarlo                      MonteCarloReport
	HostConfigSnippet               string
	Warnings                        []string
}

type MonteCarloReport struct {
	Trials             int
	Hours              float64
	MeanWins           float64
	MeanRevenueWei     *big.Int
	P50HoursPerWin     float64
	P90HoursPerWin     float64
	P99HoursPerWin     float64
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
		rep, err := analyzeOffering(off, sc.ETHUSD, sc.Simulation, baseSeed+int64(i))
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
		if _, ok := new(big.Int).SetString(off.Payout.FaceValueWei, 10); !ok {
			return fmt.Errorf("%s: invalid payout.face_value_wei %q", off.Name, off.Payout.FaceValueWei)
		}
		if off.Workload.UnitsPerHour <= 0 {
			return fmt.Errorf("%s: workload.units_per_hour must be > 0", off.Name)
		}
		if off.Workload.RequestsPerHour <= 0 {
			return fmt.Errorf("%s: workload.requests_per_hour must be > 0", off.Name)
		}
		if off.Workload.UnitsPerRequest <= 0 {
			return fmt.Errorf("%s: workload.units_per_request must be > 0", off.Name)
		}
		if off.Workload.TargetRevenuePerHourUSD <= 0 {
			return fmt.Errorf("%s: workload.target_revenue_per_hour_usd must be > 0", off.Name)
		}
		if off.Gateway.EstimatedUnitsMultiplier <= 0 {
			return fmt.Errorf("%s: gateway.estimated_units_multiplier must be > 0", off.Name)
		}
	}
	return nil
}

func analyzeOffering(off Offering, ethUSD float64, sim SimulationConfig, seed int64) (OfferingReport, error) {
	priceAmount, _ := new(big.Int).SetString(off.Price.AmountWei, 10)
	faceValue, _ := new(big.Int).SetString(off.Payout.FaceValueWei, 10)
	pricePerUnit := new(big.Rat).SetFrac(priceAmount, new(big.Int).SetUint64(off.Price.PerUnits))
	configuredRevenuePerHourWei := new(big.Rat).Mul(pricePerUnit, new(big.Rat).SetFloat64(off.Workload.UnitsPerHour))
	priceFloor := targetPriceFloor(off.Workload.TargetRevenuePerHourUSD, off.Workload.UnitsPerHour, ethUSD)
	priceFloorWhole := ceilRat(priceFloor)
	fairnessRatio := ratToFloat64(new(big.Rat).Quo(pricePerUnit, priceFloor))

	evPerRequest := new(big.Rat).Mul(pricePerUnit, new(big.Rat).SetFloat64(off.Workload.UnitsPerRequest))
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
	expectedReqPerWin := 0.0
	if off.Workload.RequestsPerHour > 0 {
		expectedReqPerWin = expectedHoursPerWin * off.Workload.RequestsPerHour
	}
	expectedUnitsPerWin := 0.0
	if ratToFloat64(pricePerUnit) > 0 {
		expectedUnitsPerWin = ratToFloat64(new(big.Rat).Quo(faceValueRat, pricePerUnit))
	}

	estimatedUnits := uint64(math.Ceil(off.Workload.UnitsPerRequest * off.Gateway.EstimatedUnitsMultiplier))
	fundedValueWei := fundedValue(priceAmount, off.Price.PerUnits, estimatedUnits)

	topupThresholdUnits := uint64(0)
	topupFundUnits := uint64(0)
	if off.Gateway.LiveTopupThresholdSeconds > 0 {
		topupThresholdUnits = uint64(math.Ceil((off.Workload.UnitsPerHour / 3600) * float64(off.Gateway.LiveTopupThresholdSeconds)))
	}
	if off.Gateway.LiveTopupFundSeconds > 0 {
		topupFundUnits = uint64(math.Ceil((off.Workload.UnitsPerHour / 3600) * float64(off.Gateway.LiveTopupFundSeconds)))
	}

	mc := runMonteCarlo(off, sim, faceValue, pricePerUnit, ticketsPerRequest, winProb, seed)
	snippet := hostConfigSnippet(off, priceFloorWhole)

	return OfferingReport{
		Offering:                         off,
		PricePerUnitWei:                 pricePerUnit,
		ConfiguredRevenuePerHourWei:     configuredRevenuePerHourWei,
		ConfiguredRevenuePerHourUSD:     weiPerHourToUSD(configuredRevenuePerHourWei, ethUSD),
		TargetPriceFloorWeiPerUnit:      priceFloor,
		TargetPriceFloorWholeWeiPerUnit: priceFloorWhole,
		FairnessRatio:                   fairnessRatio,
		WinProbabilityPerTicket:         winProb,
		TicketsPerRequest:               ticketsPerRequest,
		ExpectedWinsPerHour:             expectedWinsPerHour,
		ExpectedHoursPerWin:             expectedHoursPerWin,
		ExpectedRequestsPerWin:          expectedReqPerWin,
		ExpectedUnitsPerWin:             expectedUnitsPerWin,
		GatewayEstimatedUnits:           estimatedUnits,
		GatewayFundedValueWei:           fundedValueWei,
		GatewayLiveTopupThresholdUnits:  topupThresholdUnits,
		GatewayLiveTopupFundUnits:       topupFundUnits,
		MonteCarlo:                      mc,
		HostConfigSnippet:               snippet,
		Warnings:                        warnings,
	}, nil
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
	ethPerHour := new(big.Rat).Quo(weiPerHour, new(big.Rat).SetInt(big.NewInt(1_000_000_000_000_000_000)))
	usdPerHour := new(big.Rat).Mul(ethPerHour, new(big.Rat).SetFloat64(ethUSD))
	return ratToFloat64(usdPerHour)
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
