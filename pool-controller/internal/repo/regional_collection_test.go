package repo

import (
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"strings"
	"testing"
	"time"
)

func TestReceiptPagesFilterBeforeLimitAndRejectChangingSnapshot(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i := 0; i < 601; i++ {
		if err := store.SaveWorkReceipt(types.WorkReceipt{ID: fmt.Sprintf("a-%04d", i), RoundID: "100", Status: "final"}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 510; i++ {
		if err := store.SaveWorkReceipt(types.WorkReceipt{ID: fmt.Sprintf("z-%04d", i), RoundID: "101", Status: "final"}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.PageWorkReceipts("100", "final", "", "", 500)
	if err != nil || first.Total != 601 || len(first.Receipts) != 500 || first.NextCursor == "" {
		t.Fatalf("first page %+v err %v", first, err)
	}
	second, err := store.PageWorkReceipts("100", "final", first.NextCursor, first.Snapshot, 500)
	if err != nil || len(second.Receipts) != 101 || second.NextCursor != "" || first.Snapshot != second.Snapshot {
		t.Fatalf("second page %d %v", len(second.Receipts), err)
	}
	if err := store.SaveWorkReceipt(types.WorkReceipt{ID: "a-0000", RoundID: "100", Status: "rejected"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PageWorkReceipts("100", "final", first.NextCursor, first.Snapshot, 500); err == nil {
		t.Fatal("mixed receipt snapshots accepted")
	}
}

func TestRevenueSourceHistorySurvivesDrainAndRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	pool := store.PoolID()
	source := revenue.Source{PoolID: pool, SourceID: "0x" + strings.Repeat("a", 64), BrokerID: "audio-broker", ChainID: 42161, Payee: "0x" + strings.Repeat("b", 40), URL: "https://audio.example"}
	if err := store.RegisterRevenueSource(source, 100, "initial topology"); err != nil {
		t.Fatal(err)
	}
	other := source
	other.SourceID = "0x" + strings.Repeat("c", 64)
	other.BrokerID = "llm-broker"
	other.URL = "https://llm.example"
	if err := store.RegisterRevenueSource(other, 100, "independent same-payee receiver"); err != nil {
		t.Fatal(err)
	}
	wrong := source
	wrong.PoolID = "other"
	if err := store.RegisterRevenueSource(wrong, 100, "wrong pool"); err == nil {
		t.Fatal("foreign source accepted")
	}
	if err := store.DrainRevenueSource(source.SourceID, 120, "stop admitting, retain late redemptions"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	round := int64(1000)
	sources, err := store.RevenueSources(&round)
	if err != nil || len(sources) != 2 || sources[0].State != "draining" {
		t.Fatalf("historical obligation lost %+v %v", sources, err)
	}
	if err := store.RegisterRevenueSource(source, 121, "rewrite history"); err == nil {
		t.Fatal("source start changed")
	}
	if err := store.DrainRevenueSource(source.SourceID, 121, "rewrite stop"); err == nil {
		t.Fatal("source stop changed")
	}
	for _, record := range sources {
		if record.Source.SourceID == source.SourceID {
			if len(record.History) != 2 || record.History[0].Action != "register" || record.History[1].Action != "drain" || record.AuditReason != "initial topology" {
				t.Fatalf("lost transition provenance %+v", record)
			}
		}
	}
	round = 99
	sources, err = store.RevenueSources(&round)
	if err != nil || len(sources) != 0 {
		t.Fatal("source required before joining")
	}
}

func TestRegionalRoundRequiresEverySourceAndCompleteWorkProof(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	emptyDigest, _ := revenue.WorkDigest(nil)
	page, err := st.PageWorkReceipts("100", "final", "", "", 500)
	if err != nil {
		t.Fatal(err)
	}
	receipt := types.RoundReceipt{ID: "round-close-100", PoolID: st.PoolID(), RoundID: "100", PoolRevenueWei: "300", PoolCutWei: "0", DistributableWei: "300", ReceiptSnapshot: page.Snapshot}
	for i, name := range []string{"transcode-broker", "audio-broker", "llm-broker"} {
		source := revenue.Source{PoolID: st.PoolID(), SourceID: "0x" + strings.Repeat(fmt.Sprintf("%x", i+1), 64), BrokerID: name, ChainID: 42161, Payee: "0x" + strings.Repeat("b", 40), URL: "https://" + name + ".example"}
		if err := st.RegisterRevenueSource(source, 100, "initial regional source"); err != nil {
			t.Fatal(err)
		}
		receipt.RevenueReports = append(receipt.RevenueReports, revenue.Report{PoolID: source.PoolID, SourceID: source.SourceID, BrokerID: source.BrokerID, ChainID: source.ChainID, Payee: source.Payee, Round: 100, RevenueWei: "100", Complete: true, CompleteThroughRound: 100, ObservedAt: now, ObservedHead: 1000, ObservedHeadHash: "0x" + strings.Repeat("c", 64), FinalizedBlock: 996, FinalizedBlockHash: "0x" + strings.Repeat("d", 64), InclusionDigest: strings.Repeat("e", 64)})
		receipt.WorkReports = append(receipt.WorkReports, revenue.WorkReport{PoolID: source.PoolID, SourceID: source.SourceID, BrokerID: source.BrokerID, Round: 100, Complete: true, ClosedThroughRound: 100, ObservedAt: now, ReceiptDigest: emptyDigest})
	}
	partial := receipt
	partial.RevenueReports = partial.RevenueReports[:2]
	if err := st.SaveRegionalRound(partial); err == nil {
		t.Fatal("missing receiver became zero")
	}
	partial = receipt
	partial.WorkReports = nil
	if err := st.SaveRegionalRound(partial); err == nil {
		t.Fatal("missing receipt evidence became zero work")
	}
	partial = receipt
	partial.RevenueReports = append([]revenue.Report(nil), receipt.RevenueReports...)
	partial.RevenueReports[1] = partial.RevenueReports[0]
	if err := st.SaveRegionalRound(partial); err == nil {
		t.Fatal("duplicate receiver counted twice")
	}
	if err := st.SaveRegionalRound(receipt); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRegionalRound(receipt); err != nil {
		t.Fatal("idempotent close failed", err)
	}
	extra := receipt.RevenueReports[0]
	newSource := revenue.Source{PoolID: st.PoolID(), SourceID: "0x" + strings.Repeat("9", 64), BrokerID: "replacement", ChainID: extra.ChainID, Payee: extra.Payee, URL: "https://replacement.example"}
	if err := st.RegisterRevenueSource(newSource, 100, "late historical rewrite"); err == nil {
		t.Fatal("source registration rewrote closed history")
	}
	noncanonical := receipt
	noncanonical.ID = "round-other"
	noncanonical.RoundID = "0100"
	if err := st.SaveRegionalRound(noncanonical); err == nil {
		t.Fatal("alternate round encoding accepted")
	}
	late := types.WorkReceipt{ID: extra.SourceID + "/late", PoolID: st.PoolID(), SourceID: extra.SourceID, RoundID: "100", Status: "final", AttributedRevenueWei: "1"}
	if err := st.SaveWorkReceipt(late); err == nil {
		t.Fatal("receipt inserted into frozen round")
	}
	changed := receipt
	changed.PoolRevenueWei = "301"
	changed.DistributableWei = "301"
	if err := st.SaveRegionalRound(changed); err == nil {
		t.Fatal("closed round rewritten")
	}
	if err := st.SaveRoundReceipt(receipt); err == nil {
		t.Fatal("legacy close bypassed regional evidence")
	}

	retiring := receipt.RevenueReports[0]
	if err := st.DrainRevenueSource(retiring.SourceID, 100, "stop source", "operator"); err != nil {
		t.Fatal(err)
	}
	proof := revenue.DrainReport{PoolID: st.PoolID(), SourceID: retiring.SourceID, BrokerID: retiring.BrokerID, ChainID: retiring.ChainID, Payee: retiring.Payee, Draining: true, Frozen: true, FrozenAt: now, Complete: true, CompleteThroughRound: 101, LastRedemptionRound: 100, ObservedAt: now}
	for _, mutate := range []func(*revenue.DrainReport){func(p *revenue.DrainReport) { p.Frozen = false }, func(p *revenue.DrainReport) { p.PendingRedemptions = 1 }, func(p *revenue.DrainReport) { p.ActiveAuthorizations = 1 }, func(p *revenue.DrainReport) { p.UndeliveredReceipts = 1 }, func(p *revenue.DrainReport) { p.ObservedAt = now.Add(-time.Hour) }, func(p *revenue.DrainReport) { p.SourceID = "other" }} {
		bad := proof
		mutate(&bad)
		if err := st.RetireRevenueSource(retiring.SourceID, 100, "retire", bad, "operator"); err == nil {
			t.Fatal("incomplete source retirement accepted")
		}
	}
	if err := st.RetireRevenueSource(retiring.SourceID, 101, "retire", proof, "operator"); err == nil {
		t.Fatal("unclosed source participation round retired")
	}
	if err := st.RetireRevenueSource(retiring.SourceID, 100, "settled retirement", proof, "operator"); err != nil {
		t.Fatal(err)
	}
	beforeRound, afterRound := int64(100), int64(101)
	before, _ := st.RevenueSources(&beforeRound)
	after, _ := st.RevenueSources(&afterRound)
	if len(before) != 3 || len(after) != 2 {
		t.Fatalf("retired source history before=%d after=%d", len(before), len(after))
	}
	for _, source := range before {
		if source.Source.SourceID == retiring.SourceID && (source.RetirementProof == nil || len(source.History) != 3 || source.History[2].Actor != "operator") {
			t.Fatalf("retirement evidence missing %+v", source)
		}
	}
	if err := st.DrainRevenueSource(retiring.SourceID, 101, "reactivate"); err == nil {
		t.Fatal("retired source reactivated")
	}
}

func TestRegionalWorkReceiptRetryCannotRewriteEvidence(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	receipt := types.WorkReceipt{ID: "source/operation", PoolID: st.PoolID(), SourceID: "source", RoundID: "100", CreatedAt: time.Now().UTC(), Status: "final", AttributedRevenueWei: "42"}
	if err = st.SaveWorkReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if err = st.SaveWorkReceipt(receipt); err != nil {
		t.Fatal("identical replay failed", err)
	}
	changed := receipt
	changed.AttributedRevenueWei = "43"
	if err = st.SaveWorkReceipt(changed); err == nil {
		t.Fatal("billed value rewrite accepted")
	}
	changed = receipt
	changed.PoolID = ""
	changed.SourceID = ""
	if err = st.SaveWorkReceipt(changed); err == nil {
		t.Fatal("legacy write replaced regional receipt")
	}
	changed = receipt
	changed.ID = "other/operation"
	changed.PoolID = "other-pool"
	if err = st.SaveWorkReceipt(changed); err == nil {
		t.Fatal("foreign receipt accepted")
	}
}
