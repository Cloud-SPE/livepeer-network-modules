package repo

import (
	"strings"
	"testing"
	"time"
)

func TestRegionalCorrectionEvidenceHoldsOnlyUnapprovedAffectedWindow(t *testing.T) {
	for _, approved := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "approved"}[approved], func(t *testing.T) {
			dir := t.TempDir()
			st, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			source := windowSource(t, st)
			for n := int64(100); n < 114; n++ {
				freezeWindowRound(t, st, source, n, "10", "1")
			}
			_, batch, err := st.CloseRegionalWindow(100)
			if err != nil {
				t.Fatal(err)
			}
			if approved {
				if _, err := st.ApproveRegionalBatch(batch.ID, "operator"); err != nil {
					t.Fatal(err)
				}
			}
			original, err := st.GetRoundReceipt("round-100")
			if err != nil {
				t.Fatal(err)
			}
			changed := original
			// A changed inclusion commitment matters even when the amount is unchanged.
			changed.RevenueReports = append(changed.RevenueReports[:0:0], original.RevenueReports...)
			changed.RevenueReports[0].InclusionDigest = strings.Repeat("f", 64)
			for i := 0; i < 2; i++ {
				if err := st.SaveRegionalRound(changed); err == nil {
					t.Fatal("correction overwrote closed evidence")
				}
			}
			holds, err := st.RegionalCorrectionHolds()
			if err != nil || len(holds) != 1 {
				t.Fatalf("holds %+v %v", holds, err)
			}
			if holds[0].Accepted.RevenueReports[0].InclusionDigest != original.RevenueReports[0].InclusionDigest {
				t.Fatal("accepted evidence lost")
			}
			// Ordinary advancing observation heads are not economic corrections.
			refreshed := original
			refreshed.RevenueReports = append(refreshed.RevenueReports[:0:0], original.RevenueReports...)
			refreshed.RevenueReports[0].ObservedAt = time.Now().UTC()
			refreshed.RevenueReports[0].ObservedHead++
			if err := st.SaveRegionalRound(refreshed); err != nil {
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			result, err := st.ApproveRegionalBatch(batch.ID, "another-operator")
			if approved {
				if err != nil || result.ApprovedBy != "operator" || result.TotalAmountWei != batch.TotalAmountWei {
					t.Fatalf("approved obligation changed %+v %v", result, err)
				}
			} else if err == nil {
				t.Fatal("unapproved corrected window approved")
			}
			// A later complete window remains independent of the held one.
			for n := int64(114); n < 128; n++ {
				freezeWindowRound(t, st, source, n, "10", "1")
			}
			_, later, err := st.CloseRegionalWindow(114)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.ApproveRegionalBatch(later.ID, "operator"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
