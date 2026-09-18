package repo

import (
	"sync"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestRegionalIdentityTermsAndAcceptanceSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := r.PoolID()
	other, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if id == "" || id == other.PoolID() {
		t.Fatal("fresh pools share identity")
	}
	term := types.RegionalTerms{PoolID: id, Version: "v1", EffectiveRound: 100, CommissionBPS: 1000, ParticipationRules: "managed device participation", ZeroWorkToOperator: true, RoundingToOperator: true}
	if err := r.PutRegionalTerms(term); err != nil {
		t.Fatal(err)
	}
	if err := r.PutRegionalTerms(term); err != nil {
		t.Fatal("idempotent terms:", err)
	}
	term.CommissionBPS++
	if err := r.PutRegionalTerms(term); err == nil {
		t.Fatal("rewrote terms")
	}
	term.CommissionBPS--
	if err := other.PutRegionalTerms(term); err == nil {
		t.Fatal("accepted wrong pool terms")
	}
	wallet := "0x0000000000000000000000000000000000000011"
	if _, err := r.AcceptRegionalTerms(id, wallet, "v1"); err == nil {
		t.Fatal("unauthenticated membership")
	}
	if err := r.PutPoolMember(types.PoolMember{ID: wallet, EthAddress: wallet, Status: types.MemberStatusActive}); err != nil {
		t.Fatal(err)
	}
	accepted, err := r.AcceptRegionalTerms(id, wallet, "v1")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			again, err := r.AcceptRegionalTerms(id, wallet, "v1")
			if err != nil || again != accepted {
				t.Errorf("acceptance replay: %+v %v", again, err)
			}
		})
	}
	wg.Wait()
	if _, err := r.AcceptRegionalTerms(other.PoolID(), wallet, "v1"); err == nil {
		t.Fatal("accepted wrong region")
	}
	term.Version, term.EffectiveRound = "v2", 113
	if err := r.PutRegionalTerms(term); err == nil {
		t.Fatal("accepted overlapping window")
	}
	term.EffectiveRound = 114
	term.CommissionBPS = 2000
	if err := r.PutRegionalTerms(term); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RequireTermsAcceptance(wallet, "v2"); err == nil {
		t.Fatal("silently accepted changed terms")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.PoolID() != id {
		t.Fatal("restart changed identity")
	}
	restored, err := r.RequireTermsAcceptance(wallet, "v1")
	if err != nil || restored != accepted {
		t.Fatalf("lost acceptance: %+v %v", restored, err)
	}
	for round, rate := range map[uint64]uint64{100: 1000, 113: 1000, 114: 2000, 127: 2000} {
		terms, err := r.RegionalTermsForRound(round)
		if err != nil || terms.CommissionBPS != rate || terms.WindowRounds != 14 {
			t.Fatalf("round %d: %+v %v", round, terms, err)
		}
	}
}
