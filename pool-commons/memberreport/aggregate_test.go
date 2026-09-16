package memberreport

import (
	"strings"
	"testing"
	"time"
)

func sample(pool string) RegionalResult {
	now := time.Now()
	return RegionalResult{PoolID: pool, Name: pool, Status: "fresh", FetchedAt: now, Report: &Report{PoolID: pool, Wallet: "0x" + strings.Repeat("1", 40), ObservedAt: now, Windows: []Window{{PoolID: pool, WindowID: "same-local-id", BatchID: "batch", ChainID: 42161, Asset: "native_eth", StartRound: 100, EndRound: 113, TermsVersion: "v1", EarnedWei: "900719925474099312345", AwaitingApprovalWei: "0", PendingWei: "900719925474099312344", PaidWei: "1", Sources: []SourceEvidence{{PoolID: pool, SourceID: "source", Round: 100, InclusionDigest: "digest", WorkDigest: "work"}}}}}}
}
func TestCompatibleQualifiedExactAndUnavailable(t *testing.T) {
	eu, us := sample("eu"), sample("us")
	all, err := Combine([]RegionalResult{eu, us})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || !all[0].Complete || all[0].EarnedWei != "1801439850948198624690" || all[0].PaidWei != "2" || len(all[0].Sources) != 2 {
		t.Fatal(all)
	}
	us.Status = "stale"
	all, err = Combine([]RegionalResult{eu, us})
	if err != nil || all[0].Complete || all[0].Sources[1].Status != "stale" {
		t.Fatal(all, err)
	}
	us.Status = "unavailable"
	us.Report = nil
	all, err = Combine([]RegionalResult{eu, us})
	if err != nil || all[0].Complete || len(all[0].MissingPools) != 1 || all[0].MissingPools[0] != "us" || all[0].EarnedWei != eu.Report.Windows[0].EarnedWei {
		t.Fatal(all, err)
	}
}
func TestPeriodsDenominationsAndMemberNeverMixed(t *testing.T) {
	for _, change := range []func(*Window){func(w *Window) { w.StartRound = 99 }, func(w *Window) { w.EndRound = 114 }, func(w *Window) { w.Asset = "other" }, func(w *Window) { w.ChainID = 1 }} {
		eu, us := sample("eu"), sample("us")
		change(&us.Report.Windows[0])
		all, err := Combine([]RegionalResult{eu, us})
		if err != nil || len(all) != 2 || all[0].Complete || all[1].Complete {
			t.Fatal(all, err)
		}
	}
	eu, us := sample("eu"), sample("us")
	us.Report.Wallet = "different"
	if _, err := Combine([]RegionalResult{eu, us}); err == nil {
		t.Fatal("wallets combined")
	}
	us = sample("us")
	us.Report.Windows[0].PendingWei = "0"
	if _, err := Combine([]RegionalResult{eu, us}); err == nil {
		t.Fatal("invalid category conservation")
	}
	if _, err := Combine([]RegionalResult{eu, eu}); err == nil {
		t.Fatal("same region counted twice")
	}
}
