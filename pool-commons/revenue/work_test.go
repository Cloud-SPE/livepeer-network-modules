package revenue

import "testing"

func TestWorkDigestBindsTermsVersion(t *testing.T) {
	entries := []Contribution{{ID: "source/work", PoolID: "pool", SourceID: "source", RoundID: "100", Member: "member", Offering: "offer", Backend: "host", AmountWei: "7", TermsVersion: "v1"}}
	first, err := WorkDigest(entries)
	if err != nil {
		t.Fatal(err)
	}
	entries[0].TermsVersion = "v2"
	second, err := WorkDigest(entries)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("terms substitution did not change work proof")
	}
}
