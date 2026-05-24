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
