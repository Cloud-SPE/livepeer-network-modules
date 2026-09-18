package repo

import (
	"encoding/json"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"os"
	"testing"
)

// Invoked only by the black-box e2e test before the controller starts. It seeds
// operator-published terms and optional accounting-fixture membership/sources.
// It never seeds financial results. Publication's
// broker barrier is exercised separately from the portal authentication test.
func TestRegionalE2EProvisionTerms(t *testing.T) {
	path := os.Getenv("REGIONAL_E2E_PROVISION")
	if path == "" {
		t.Skip("explicit offline e2e fixture only")
	}
	var cfg struct {
		DataDir, Version, Output, Wallet string
		Sources                          []revenue.Source
		Wallets                          []string
		Enrollments                      []types.HostEnrollment
		Hardware                         []types.HardwareUnit
		Assignments                      []types.TemplateAssignment
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir == "" || cfg.Version == "" || cfg.Output == "" {
		t.Fatal("incomplete fixture")
	}
	st, err := Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.PutRegionalTerms(types.RegionalTerms{PoolID: st.PoolID(), Version: cfg.Version, EffectiveRound: 100, WindowRounds: 14, CommissionBPS: 1000, ParticipationRules: "Regional acceptance fixture", ZeroWorkToOperator: true, RoundingToOperator: true}); err != nil {
		t.Fatal(err)
	}
	for _, wallet := range cfg.Wallets {
		if _, err = st.JoinRegionalTerms(st.PoolID(), wallet, cfg.Version); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range cfg.Sources {
		if err = st.RegisterRevenueSource(source, 100, "offline regional accounting fixture"); err != nil {
			t.Fatal(err)
		}
	}
	if cfg.Wallet != "" {
		if _, err = st.JoinRegionalTerms(st.PoolID(), cfg.Wallet, cfg.Version); err != nil {
			t.Fatal(err)
		}
	}
	for _, enrollment := range cfg.Enrollments {
		if err = st.PutHostEnrollment(enrollment); err != nil {
			t.Fatal(err)
		}
	}
	for _, unit := range cfg.Hardware {
		if err = st.SaveHardwareOwnership(unit); err != nil {
			t.Fatal(err)
		}
	}
	for _, assignment := range cfg.Assignments {
		if err = st.PutTemplateAssignment(assignment); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ = json.Marshal(map[string]string{"pool_id": st.PoolID()})
	if err = os.WriteFile(cfg.Output, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
