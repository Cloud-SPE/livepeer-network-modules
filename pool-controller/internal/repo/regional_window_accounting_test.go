package repo

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func windowSource(t *testing.T, st *StateRepo) revenue.Source {
	t.Helper()
	source := revenue.Source{PoolID: st.PoolID(), SourceID: "0x" + strings.Repeat("a", 64), BrokerID: "transcode-broker", ChainID: 42161, Payee: "0x" + strings.Repeat("b", 40), URL: "https://broker.example"}
	if err := st.RegisterRevenueSource(source, 100, "initial source"); err != nil {
		t.Fatal(err)
	}
	if err := st.PutRegionalTerms(types.RegionalTerms{PoolID: st.PoolID(), Version: "v1", EffectiveRound: 100, CommissionBPS: 1000, ParticipationRules: "regional Model B", ZeroWorkToOperator: true, RoundingToOperator: true}); err != nil {
		t.Fatal(err)
	}
	return source
}
func freezeWindowRound(t *testing.T, st *StateRepo, source revenue.Source, round int64, amount string, weights ...string) {
	t.Helper()
	var contributions []revenue.Contribution
	var ids []string
	for i, weight := range weights {
		r := types.WorkReceipt{ID: source.SourceID + fmt.Sprintf("/%d-%d", round, i), PoolID: st.PoolID(), SourceID: source.SourceID, RoundID: fmt.Sprint(round), MemberEthAddress: fmt.Sprintf("0x%040x", i+1), BackendID: "backend", OfferingID: "offering", Status: "final", AttributedRevenueWei: weight}
		term, err := st.RegionalTermsForRound(uint64(round))
		if err != nil {
			t.Fatal(err)
		}
		r.TermsVersion = term.Version
		if err := st.PutPoolMember(types.PoolMember{ID: r.MemberEthAddress, EthAddress: r.MemberEthAddress}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.AcceptRegionalTerms(st.PoolID(), r.MemberEthAddress, r.TermsVersion); err != nil {
			t.Fatal(err)
		}
		r.CreatedAt = time.Now().UTC()
		if err := st.SaveWorkReceipt(r); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
		contributions = append(contributions, revenue.Contribution{TermsVersion: r.TermsVersion, ID: r.ID, PoolID: r.PoolID, SourceID: r.SourceID, RoundID: r.RoundID, Member: r.MemberEthAddress, Offering: r.OfferingID, Backend: r.BackendID, AmountWei: weight})
	}
	digest, err := revenue.WorkDigest(contributions)
	if err != nil {
		t.Fatal(err)
	}
	page, err := st.PageWorkReceipts(fmt.Sprint(round), "final", "", "", 500)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	r := types.RoundReceipt{ID: fmt.Sprintf("round-%d", round), PoolID: st.PoolID(), RoundID: fmt.Sprint(round), PoolRevenueWei: amount, PoolCutWei: "0", DistributableWei: amount, ReceiptSnapshot: page.Snapshot, IncludedWorkReceiptIDs: ids,
		RevenueReports: []revenue.Report{{PoolID: source.PoolID, SourceID: source.SourceID, BrokerID: source.BrokerID, ChainID: source.ChainID, Payee: source.Payee, Round: round, RevenueWei: amount, Complete: true, CompleteThroughRound: round, ObservedAt: now, ObservedHead: 1000, ObservedHeadHash: "0x" + strings.Repeat("c", 64), FinalizedBlock: 996, FinalizedBlockHash: "0x" + strings.Repeat("d", 64), InclusionDigest: strings.Repeat("e", 64)}},
		WorkReports:    []revenue.WorkReport{{PoolID: source.PoolID, SourceID: source.SourceID, BrokerID: source.BrokerID, Round: round, Complete: true, ClosedThroughRound: round, ObservedAt: now, ReceiptCount: uint64(len(weights)), ReceiptDigest: digest}}}
	if err := st.SaveRegionalRound(r); err != nil {
		t.Fatal(err)
	}
}
func TestRegionalWindowAtomicFullPotReplayAndImmutableApproval(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	source := windowSource(t, st)
	freezeWindowRound(t, st, source, 100, "10", "3", "1")
	for n := int64(101); n < 113; n++ {
		freezeWindowRound(t, st, source, n, "10")
	}
	if _, _, err := st.CloseRegionalWindow(100); err == nil {
		t.Fatal("missing last round closed")
	}
	windows, _ := st.ListSettlementWindows()
	batches, _ := st.ListPayoutBatches()
	if len(windows) != 0 || len(batches) != 0 {
		t.Fatal("partial window persisted")
	}
	if _, _, err := st.CloseRegionalWindow(101); err == nil {
		t.Fatal("misaligned window closed")
	}
	freezeWindowRound(t, st, source, 113, "10")
	w, b, err := st.CloseRegionalWindow(100)
	if err != nil {
		t.Fatal(err)
	}
	if w.LengthRounds != 14 || w.TermsVersion != "v1" || w.RegionalAllocation.CommissionWei != "14" || w.RegionalAllocation.RoundingResidualWei != "1" || b.TotalAmountWei != "125" || len(b.LineItems) != 2 || b.LineItems[0].AmountWei != "94" || b.LineItems[1].AmountWei != "31" {
		t.Fatalf("allocation %+v %+v", w, b)
	}
	b.Status = types.PayoutBatchApproved
	b.ApprovedBy = "operator"
	b.ApprovedAt = time.Now().UTC()
	if _, err := st.ApproveRegionalBatch(b.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	w, err = st.GetSettlementWindow(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err = st.GetPayoutBatch(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	bad := b
	bad.TotalAmountWei = "1"
	if err := st.PutPayoutBatch(bad); err == nil {
		t.Fatal("approved economics rewritten")
	}
	bad = b
	bad.PoolID = ""
	if err := st.PutPayoutBatch(bad); err == nil {
		t.Fatal("regional guard stripped")
	}
	changed := w
	changed.ConfirmedRevenueWei = "999"
	if err := st.PutSettlementWindow(changed); err == nil {
		t.Fatal("window economics rewritten")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	again, replay, err := st.CloseRegionalWindow(100)
	if err != nil || !sameJSON(w, again) || !sameJSON(b, replay) {
		t.Fatalf("restart changed allocation/approval %+v %+v %v", again, replay, err)
	}
	term := types.RegionalTerms{PoolID: st.PoolID(), Version: "retroactive", EffectiveRound: 100, WindowRounds: 14, CommissionBPS: 2000, ParticipationRules: "regional", ZeroWorkToOperator: true, RoundingToOperator: true}
	if err := st.PutRegionalTerms(term); err == nil {
		t.Fatal("terms rewrote reconciled history")
	}
	term.Version = "v2"
	term.EffectiveRound = 114
	term.WindowRounds = 2
	if err := st.PutRegionalTerms(term); err != nil {
		t.Fatal(err)
	}
	freezeWindowRound(t, st, source, 114, "7")
	freezeWindowRound(t, st, source, 115, "8")
	zero, empty, err := st.CloseRegionalWindow(114)
	if err != nil || zero.TermsVersion != "v2" || zero.LengthRounds != 2 || zero.RegionalAllocation.ZeroWorkOperatorWei != "15" || zero.RegionalAllocation.CommissionWei != "0" || len(empty.LineItems) != 0 || empty.TotalAmountWei != "0" {
		t.Fatalf("zero work %+v %+v %v", zero, empty, err)
	}
}

func TestRegionalSchedulerKeepsMissingRoundHoldAndRebuildsTelemetry(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	source := windowSource(t, st)
	for n := int64(101); n <= 127; n++ {
		freezeWindowRound(t, st, source, n, "10")
	}
	if err := st.CloseReadyRegionalWindows(); err != nil {
		t.Fatal(err)
	}
	holds, err := st.RegionalWindowHolds()
	if err != nil || len(holds) != 1 || holds[0].StartRound != 100 || !strings.Contains(holds[0].Reason, "100") {
		t.Fatalf("hold %+v %v", holds, err)
	}
	summary, err := st.RegionalAccountingSummary()
	if err != nil || summary.CompletedWindows != 1 || summary.ZeroWorkRevenueWindows != 1 || summary.ConfirmedRevenueWei != "140" || summary.ZeroWorkOperatorWei != "140" {
		t.Fatalf("summary %+v %v", summary, err)
	}
	freezeWindowRound(t, st, source, 100, "20", "3")
	for i := 0; i < 3; i++ {
		if err := st.CloseReadyRegionalWindows(); err != nil {
			t.Fatal(err)
		}
	}
	holds, err = st.RegionalWindowHolds()
	if err != nil || len(holds) != 0 {
		t.Fatalf("resolved hold retained %+v %v", holds, err)
	}
	summary, err = st.RegionalAccountingSummary()
	if err != nil || summary.CompletedWindows != 2 || summary.ZeroWorkRevenueWindows != 1 || summary.ConfirmedRevenueWei != "290" || summary.ZeroWorkOperatorWei != "140" || summary.CommissionWei != "15" || summary.MemberPayoutWei != "135" {
		t.Fatalf("replay inflated totals %+v %v", summary, err)
	}
}
