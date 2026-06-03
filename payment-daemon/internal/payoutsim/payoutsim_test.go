package payoutsim

import "testing"

func TestGenerate(t *testing.T) {
	sc := Scenario{
		Version: 1,
		Name:    "test",
		ETHUSD:  3500,
		Seed:    7,
		Simulation: SimulationConfig{
			Hours:  24,
			Trials: 50,
		},
		Offerings: []Offering{{
			Name:         "chat",
			CapabilityID: "openai:chat-completions",
			OfferingID:   "qwen3-8b",
			Interaction:  "http-reqresp@v0",
			WorkUnit:     "tokens",
			Price:        Price{AmountWei: "2000000000000", PerUnits: 1},
			Payout:       Payout{FaceValueWei: "50000000000000000"},
			Workload: Workload{
				UnitsPerHour:            720000,
				RequestsPerHour:         360,
				UnitsPerRequest:         2000,
				TargetRevenuePerHourUSD: 2,
			},
			Gateway: GatewayPolicy{
				EstimatedUnitsMultiplier: 1.1,
			},
		}},
	}
	rep, err := Generate(sc)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(rep.Offerings) != 1 {
		t.Fatalf("offerings = %d; want 1", len(rep.Offerings))
	}
	got := rep.Offerings[0]
	if got.ExpectedWinsPerHour <= 0 {
		t.Fatalf("ExpectedWinsPerHour = %f; want > 0", got.ExpectedWinsPerHour)
	}
	if got.GatewayEstimatedUnits != 2200 {
		t.Fatalf("GatewayEstimatedUnits = %d; want 2200", got.GatewayEstimatedUnits)
	}
	if got.TargetPriceFloorWholeWeiPerUnit.Sign() <= 0 {
		t.Fatalf("TargetPriceFloorWholeWeiPerUnit = %s; want > 0", got.TargetPriceFloorWholeWeiPerUnit)
	}
	if got.MonteCarlo.Trials != 50 {
		t.Fatalf("MonteCarlo.Trials = %d; want 50", got.MonteCarlo.Trials)
	}
}

func TestGenerateUsesScenarioPayoutPolicy(t *testing.T) {
	sc := Scenario{
		Version: 1,
		Name:    "policy",
		ETHUSD:  3500,
		Seed:    7,
		Simulation: SimulationConfig{
			Hours:  24,
			Trials: 50,
		},
		PayoutPolicy: PayoutPolicy{
			TargetFaceValueWei:    "5400000000000000",
			SenderMaxFaceValueWei: "5400000000000000",
			GasPriceWei:           "40000000",
			RedeemGas:             500000,
		},
		Offerings: []Offering{{
			Name:         "chat",
			CapabilityID: "openai:chat-completions",
			OfferingID:   "qwen3",
			Interaction:  "http-reqresp@v0",
			WorkUnit:     "tokens",
			Price:        Price{AmountWei: "125000000000", PerUnits: 1000},
			Workload: Workload{
				UnitsPerHour:            720000,
				RequestsPerHour:         360,
				UnitsPerRequest:         2000,
				TargetRevenuePerHourUSD: 0.1,
			},
			Gateway: GatewayPolicy{
				EstimatedUnitsMultiplier: 1.15,
			},
			Hardware: []HardwareProfile{{
				Name:          "gpu",
				UnitsPerHour:  720000,
				HourlyCostUSD: 0.1,
			}},
		}},
	}
	rep, err := Generate(sc)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	got := rep.Offerings[0]
	if got.FaceValueWei.String() != "5400000000000000" {
		t.Fatalf("FaceValueWei = %s; want 5400000000000000", got.FaceValueWei)
	}
	if got.RedemptionCostWei.String() != "20000000000000" {
		t.Fatalf("RedemptionCostWei = %s; want 20000000000000", got.RedemptionCostWei)
	}
	if got.RedemptionMargin != 270 {
		t.Fatalf("RedemptionMargin = %f; want 270", got.RedemptionMargin)
	}
	if got.GatewayFundingToEVRatio <= 1 {
		t.Fatalf("GatewayFundingToEVRatio = %f; want > 1", got.GatewayFundingToEVRatio)
	}
	if got.ExpectedValuePerRequestWei.FloatString(0) != "250000000000" {
		t.Fatalf("ExpectedValuePerRequestWei = %s; want 250000000000", got.ExpectedValuePerRequestWei.FloatString(0))
	}
	if len(got.Hardware) != 1 {
		t.Fatalf("Hardware len = %d; want 1", len(got.Hardware))
	}
	if got.Hardware[0].ExpectedPayout7DayUSD <= 0 {
		t.Fatalf("ExpectedPayout7DayUSD = %f; want > 0", got.Hardware[0].ExpectedPayout7DayUSD)
	}
	if got.Hardware[0].BreakEvenUnitsPerHour <= 0 {
		t.Fatalf("BreakEvenUnitsPerHour = %f; want > 0", got.Hardware[0].BreakEvenUnitsPerHour)
	}
}

func TestGenerateDerivesWorkloadFromTokenMix(t *testing.T) {
	sc := Scenario{
		Version: 1,
		Name:    "token mix",
		ETHUSD:  1990,
		Seed:    9,
		Simulation: SimulationConfig{
			Hours:  24,
			Trials: 50,
		},
		PayoutPolicy: PayoutPolicy{
			TargetFaceValueWei: "5400000000000000",
		},
		Offerings: []Offering{{
			Name:         "chat",
			CapabilityID: "openai:chat-completions",
			OfferingID:   "qwen3",
			Interaction:  "http-stream@v0",
			WorkUnit:     "tokens",
			Price:        Price{AmountWei: "125000000000", PerUnits: 1000},
			Workload: Workload{
				RequestsPerHour: 360,
				TokenMix: TokenMix{
					InputTokensPerDay:  6000000,
					OutputTokensPerDay: 4000000,
					OutputWeight:       3,
				},
				TargetRevenuePerHourUSD: 0.1,
			},
			Gateway: GatewayPolicy{
				EstimatedUnitsMultiplier: 1.15,
			},
		}},
	}
	rep, err := Generate(sc)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	got := rep.Offerings[0]
	if got.TokenMix == nil {
		t.Fatal("TokenMix = nil; want derived token mix report")
	}
	if got.TokenMix.BillableTokensPerDay != 18000000 {
		t.Fatalf("BillableTokensPerDay = %f; want 18000000", got.TokenMix.BillableTokensPerDay)
	}
	if got.BillableUnitsPerHour != 750000 {
		t.Fatalf("BillableUnitsPerHour = %f; want 750000", got.BillableUnitsPerHour)
	}
	if got.BillableUnitsPerRequest < 2083 || got.BillableUnitsPerRequest > 2084 {
		t.Fatalf("BillableUnitsPerRequest = %f; want about 2083.33", got.BillableUnitsPerRequest)
	}
	if got.ExpectedValuePerRequestWei.FloatString(0) != "260416666667" {
		t.Fatalf("ExpectedValuePerRequestWei = %s; want 260416666667", got.ExpectedValuePerRequestWei.FloatString(0))
	}
}

func TestLoadScenarioRejectsInvalid(t *testing.T) {
	_, err := Generate(Scenario{
		Version: 1,
		Simulation: SimulationConfig{
			Hours:  1,
			Trials: 1,
		},
		Offerings: []Offering{{
			Name: "bad",
		}},
	})
	if err == nil {
		t.Fatal("Generate() err = nil; want validation error")
	}
}
