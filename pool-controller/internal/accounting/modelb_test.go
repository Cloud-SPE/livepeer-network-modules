package accounting

import (
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func work(id, member int, amount string) types.WorkReceipt {
	return types.WorkReceipt{PoolID: "pool", SourceID: "source", ID: fmt.Sprintf("source/%d", id), MemberEthAddress: fmt.Sprintf("0x%040x", member), OfferingID: fmt.Sprintf("offering-%d", id), BackendID: "backend", Status: "final", AttributedRevenueWei: amount}
}

func TestModelBFullPotAggregatesBeforeFloor(t *testing.T) {
	for _, tc := range []struct {
		revenue                string
		bps                    uint64
		a, b, commission, dust string
	}{
		{"1000", 1000, "675", "225", "100", "0"},
		{"10", 1000, "6", "2", "1", "1"},
		{"3", 0, "2", "0", "0", "1"},
		{"1", 1000, "0", "0", "0", "1"},
		{"1000", 10000, "0", "0", "1000", "0"},
		{"0", 1000, "0", "0", "0", "0"},
	} {
		t.Run(tc.revenue+fmt.Sprint(tc.bps), func(t *testing.T) {
			a, err := ModelB("pool", tc.revenue, tc.bps, []types.WorkReceipt{work(1, 1, "1"), work(2, 2, "1"), work(3, 1, "2")})
			if err != nil {
				t.Fatal(err)
			}
			if len(a.Members) != 2 || a.Members[0].AmountWei != tc.a || a.Members[1].AmountWei != tc.b || a.CommissionWei != tc.commission || a.RoundingResidualWei != tc.dust {
				t.Fatalf("allocation %+v", a)
			}
			if a.Members[0].AttributedRevenueWei != "3" || a.Members[0].OfferingID != "" {
				t.Fatal("not aggregated across offerings")
			}
		})
	}
}
func TestModelBTinyShareLargeValueIsNotTruncatedToPPM(t *testing.T) {
	a, err := ModelB("pool", "1000000000000000000000000000000000000", 0, []types.WorkReceipt{work(1, 1, "1"), work(2, 2, "9999999")})
	if err != nil {
		t.Fatal(err)
	}
	if a.Members[0].AmountWei != "100000000000000000000000000000" {
		t.Fatalf("fraction lost %+v", a)
	}
}
func TestModelBZeroWorkExceptionAndInvalidEvidence(t *testing.T) {
	for _, rs := range [][]types.WorkReceipt{nil, {work(1, 1, "0")}} {
		a, err := ModelB("pool", "123", 1000, rs)
		if err != nil || a.ZeroWorkOperatorWei != "123" || a.CommissionWei != "0" || a.MemberPayoutWei != "0" || a.RoundingResidualWei != "0" || len(a.Members) != 0 {
			t.Fatalf("zero work %+v %v", a, err)
		}
	}
	for _, change := range []func(*types.WorkReceipt){
		func(r *types.WorkReceipt) { r.PoolID = "other" }, func(r *types.WorkReceipt) { r.Status = "accepted" }, func(r *types.WorkReceipt) { r.ID = "unqualified" }, func(r *types.WorkReceipt) { r.MemberEthAddress = "" }, func(r *types.WorkReceipt) { r.AttributedRevenueWei = "-1" }, func(r *types.WorkReceipt) { r.AttributedRevenueWei = "" }, func(r *types.WorkReceipt) { r.AttributedRevenueWei = "01" },
	} {
		r := work(1, 1, "1")
		change(&r)
		if _, err := ModelB("pool", "100", 0, []types.WorkReceipt{r}); err == nil {
			t.Fatalf("invalid evidence accepted %+v", r)
		}
	}
	r := work(1, 1, "1")
	if _, err := ModelB("pool", "100", 0, []types.WorkReceipt{r, r}); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := ModelB("pool", "100", 10001, nil); err == nil {
		t.Fatal("invalid commission accepted")
	}
}
func TestModelBConservationAndDustBound(t *testing.T) {
	random := rand.New(rand.NewSource(123))
	for i := 0; i < 500; i++ {
		n := 1 + random.Intn(30)
		var rs []types.WorkReceipt
		for j := 0; j < n; j++ {
			rs = append(rs, work(j, j, fmt.Sprint(1+random.Intn(10000000))))
		}
		r := new(big.Int).Mul(new(big.Int).SetUint64(random.Uint64()), new(big.Int).SetUint64(random.Uint64()))
		a, err := ModelB("pool", r.String(), uint64(random.Intn(10001)), rs)
		if err != nil {
			t.Fatal(err)
		}
		paid, _ := Wei(a.MemberPayoutWei)
		c, _ := Wei(a.CommissionWei)
		dust, _ := Wei(a.RoundingResidualWei)
		sum := new(big.Int).Add(paid, c)
		sum.Add(sum, dust)
		if sum.Cmp(r) != 0 || dust.Cmp(big.NewInt(int64(n))) >= 0 {
			t.Fatalf("conservation/dust %+v", a)
		}
	}
}

func TestModelBProtocolFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../livepeer-network-protocol/conformance/fixtures/regional-model-b.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Revenue    string `json:"revenue_wei"`
		Commission uint64 `json:"commission_bps"`
		Receipts   []struct {
			Member int    `json:"member"`
			Billed string `json:"billed_wei"`
		} `json:"receipts"`
		ExpectedCommission string   `json:"expected_commission_wei"`
		ExpectedMembers    []string `json:"expected_member_payouts_wei"`
		ExpectedResidual   string   `json:"expected_rounding_residual_wei"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	var receipts []types.WorkReceipt
	for i, r := range fixture.Receipts {
		receipts = append(receipts, work(i, r.Member, r.Billed))
	}
	a, err := ModelB("pool", fixture.Revenue, fixture.Commission, receipts)
	if err != nil {
		t.Fatal(err)
	}
	if a.CommissionWei != fixture.ExpectedCommission || a.RoundingResidualWei != fixture.ExpectedResidual || len(a.Members) != len(fixture.ExpectedMembers) {
		t.Fatalf("fixture %+v", a)
	}
	for i, want := range fixture.ExpectedMembers {
		if a.Members[i].AmountWei != want {
			t.Fatalf("member %d: %s want %s", i, a.Members[i].AmountWei, want)
		}
	}
}
