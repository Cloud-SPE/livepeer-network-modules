package repo

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberreport"
)

func TestRegionalMemberReportApprovalPaymentPrivacyAndGaps(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	source := windowSource(t, st)
	for round := int64(100); round <= 113; round++ {
		freezeWindowRound(t, st, source, round, "100", "3", "1")
	}
	_, batch, err := st.CloseRegionalWindow(100)
	if err != nil {
		t.Fatal(err)
	}
	freezeWindowRound(t, st, source, 128, "1")
	wallet := fmt.Sprintf("0x%040x", 1)
	report, err := st.RegionalMemberReport(wallet)
	if err != nil {
		t.Fatal(err)
	}
	if err := memberreport.Validate(report, st.PoolID(), wallet); err != nil {
		t.Fatal(err)
	}
	if len(report.CompleteRoundSpans) != 2 || report.CompleteRoundSpans[0].End != 113 || report.CompleteRoundSpans[1].Start != 128 {
		t.Fatal("gap concealed", report.CompleteRoundSpans)
	}
	row := report.Windows[0]
	if row.EarnedWei != "945" || row.AwaitingApprovalWei != "945" || row.PendingWei != "0" || row.PaidWei != "0" || len(row.Sources) != 14 {
		t.Fatalf("wrong pending approval %+v", row)
	}
	raw, _ := json.Marshal(report)
	if strings.Contains(string(raw), fmt.Sprintf("0x%040x", 2)) {
		t.Fatal("other wallet disclosed")
	}
	if _, err := st.ApproveRegionalBatch(batch.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	report, err = st.RegionalMemberReport(wallet)
	if err != nil {
		t.Fatal(err)
	}
	row = report.Windows[0]
	if row.AwaitingApprovalWei != "0" || row.PendingWei != "945" || row.PaidWei != "0" {
		t.Fatal("approval miscategorized", row)
	}
	intents, err := st.ListPayoutIntents(0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, intent := range intents {
		if intent.MemberEthAddress == wallet {
			found = true
			intent.Status = "failed"
			intent.FailureReason = "operator funding unavailable"
			if err := st.SavePayoutIntent(intent); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found {
		t.Fatal("intent missing")
	}
	report, err = st.RegionalMemberReport(wallet)
	if err != nil || report.Windows[0].PendingWei != "945" {
		t.Fatal("failed payment erased debt", err)
	}
	intents, _ = st.ListPayoutIntents(0)
	for _, intent := range intents {
		if intent.MemberEthAddress == wallet {
			intent.Status = "paid"
			intent.PaidAt = time.Now().UTC()
			intent.TxHash = "0x" + strings.Repeat("a", 64)
			if err := st.SavePayoutIntent(intent); err != nil {
				t.Fatal(err)
			}
		}
	}
	report, err = st.RegionalMemberReport(wallet)
	if err != nil {
		t.Fatal(err)
	}
	row = report.Windows[0]
	if row.PendingWei != "0" || row.PaidWei != "945" || row.EarnedWei != "945" {
		t.Fatal("paid double counted", row)
	}
	other, err := st.RegionalMemberReport(fmt.Sprintf("0x%040x", 99))
	if err != nil || len(other.Windows) != 1 || other.Windows[0].EarnedWei != "0" || len(other.Windows[0].Payments) != 0 {
		t.Fatal("nonparticipant lacks qualified zero or leaks payment", err)
	}
}
