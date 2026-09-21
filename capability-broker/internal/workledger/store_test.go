package workledger

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/receipts"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "work.db")
	s, err := Open(path, "pool", "source", "broker")
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}
func bind(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.Bind(id, Attribution{Member: "member", Backend: "host|gpu", Enrollment: "host", Capability: "cap", Offering: "offer", RequestID: id}); err != nil {
		t.Fatal(err)
	}
}
func TestDurablePartialBillingAndCompleteOutbox(t *testing.T) {
	s, path := testStore(t)
	now := time.Now().UTC()
	bind(t, s, "session")

	var fixture struct {
		Operations []struct {
			Kind     string `json:"kind"`
			Sequence uint64 `json:"sequence"`
			Units    uint64 `json:"cumulative_units"`
			Total    string `json:"cumulative_billed_wei"`
		} `json:"operations"`
		Count int    `json:"expected_receipt_count"`
		Total string `json:"expected_total_wei"`
	}
	raw, err := os.ReadFile("../../../livepeer-network-protocol/conformance/fixtures/regional-billed-work.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, event := range fixture.Operations {
		op, err := s.Prepare(Operation{AuthorizationID: "session", Kind: event.Kind, Sequence: event.Sequence, Units: event.Units})
		if err != nil {
			t.Fatal(err)
		}
		total, ok := new(big.Int).SetString(event.Total, 10)
		if !ok {
			t.Fatal("invalid fixture amount")
		}
		if err = s.Complete(op.ID, total); err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < 601; i++ {
		id := fmt.Sprintf("job-%03d", i)
		bind(t, s, id)
		op, err := s.Prepare(Operation{AuthorizationID: id, Kind: "settle", Sequence: 1, Units: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Complete(op.ID, big.NewInt(1)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Finalize(100, now); err != nil {
		t.Fatal(err)
	}
	if r, err := s.Report(100, now); err != nil || r.Complete {
		t.Fatalf("open round complete: %+v %v", r, err)
	}
	if err := s.Finalize(101, now); err != nil {
		t.Fatal(err)
	}
	before, err := s.Report(100, now)
	if err != nil || !before.Complete || before.ReceiptCount != uint64(601+fixture.Count) {
		t.Fatalf("report %+v %v", before, err)
	}
	all, err := s.Undelivered(0)
	if err != nil {
		t.Fatal(err)
	}
	total := new(big.Int)
	for _, r := range all {
		n, _ := new(big.Int).SetString(r.AttributedRevenueWei, 10)
		total.Add(total, n)
	}
	expected, _ := new(big.Int).SetString(fixture.Total, 10)
	expected.Add(expected, big.NewInt(601))
	if total.Cmp(expected) != 0 {
		t.Fatalf("inflated cumulative billing: %s", total)
	}
	first, _ := s.Undelivered(500)
	if len(first) != 500 {
		t.Fatal(len(first))
	}
	for _, r := range first {
		if err = s.Delivered(r.ID); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = Open(path, "pool", "source", "broker")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	remaining, _ := s.Undelivered(500)
	if len(remaining) != 103 {
		t.Fatal(len(remaining))
	}
	after, err := s.Report(100, now)
	if err != nil || before.ReceiptDigest != after.ReceiptDigest {
		t.Fatalf("restart changed evidence: %v", err)
	}
	if r, _ := s.Report(100, now.Add(3*time.Minute)); r.Complete {
		t.Fatal("stale report complete")
	}
	if err = s.Finalize(99, now); err == nil {
		t.Fatal("regressed clock accepted")
	}
}
func TestUncertainBillingCannotProveZero(t *testing.T) {
	s, _ := testStore(t)
	defer s.Close()
	now := time.Now()
	bind(t, s, "job")
	op, err := s.Prepare(Operation{AuthorizationID: "job", Kind: "settle", Sequence: 1, Units: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Finalize(101, now); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Report(100, now); r.Complete {
		t.Fatal("uncertain billing became zero work")
	}
	if err = s.Complete(op.ID, big.NewInt(3)); err != nil {
		t.Fatal(err)
	}
	if err = s.Finalize(101, now); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Report(100, now); !r.Complete || r.ReceiptCount != 0 {
		t.Fatalf("resolved earlier zero: %+v", r)
	}
	if _, err = s.Prepare(Operation{AuthorizationID: "job", Kind: "settle", Sequence: 1, Units: 2}); err == nil {
		t.Fatal("changed replay accepted")
	}
	if err = s.Complete(op.ID, big.NewInt(4)); err == nil {
		t.Fatal("changed billing accepted")
	}
	if err = s.Bind("job", Attribution{Member: "other", Backend: "host|gpu", Enrollment: "host", Capability: "cap", Offering: "offer"}); err == nil {
		t.Fatal("member rewrite accepted")
	}
	if _, err = s.Prepare(Operation{AuthorizationID: "unbound", Kind: "settle", Sequence: 1, Units: 1}); err == nil {
		t.Fatal("unattributed billing accepted")
	}
}

type receiver struct {
	payment.AccountClient
	fail  bool
	calls int
	now   time.Time
}

func (r *receiver) SettleAuthorization(_ context.Context, req payment.SettleAuthorizationRequest) (*payment.SettleAuthorizationResult, error) {
	r.calls++
	if req.AuthorizationID != "job" || req.SettlementSeq != 1 || req.ActualUnits != 5 {
		return nil, fmt.Errorf("replay changed")
	}
	if r.fail {
		r.fail = false
		return nil, fmt.Errorf("response lost after receiver billed")
	}
	return &payment.SettleAuthorizationResult{State: int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED), Billed: big.NewInt(13)}, nil
}
func (r *receiver) RoundRevenue(context.Context, int64) (*pb.GetRoundRevenueResponse, error) {
	return &pb.GetRoundRevenueResponse{SettlementDomainId: "source", CompleteThroughRound: 100, ObservedAt: r.now.Format(time.RFC3339Nano)}, nil
}
func (r *receiver) AdmitAuthorization(context.Context, payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	return &payment.AdmitAuthorizationResult{}, nil
}

type sink struct {
	fail  bool
	items map[string]receipts.WorkReceipt
}

func (s *sink) UpsertWorkReceipt(_ context.Context, r receipts.WorkReceipt) error {
	if s.fail {
		return fmt.Errorf("controller offline")
	}
	s.items[r.ID] = r
	return nil
}
func TestLostReceiverResponseAndControllerOutageAcrossRestart(t *testing.T) {
	s, path := testStore(t)
	bind(t, s, "job")
	remote := &receiver{fail: true, now: time.Now()}
	out := &sink{fail: true, items: map[string]receipts.WorkReceipt{}}
	c := &Client{AccountClient: remote, Reader: remote, Store: s, Sink: out}
	if _, err := c.SettleAuthorization(context.Background(), payment.SettleAuthorizationRequest{AuthorizationID: "job", ActualUnits: 5, SettlementSeq: 1}); err == nil {
		t.Fatal("expected response loss")
	}
	s.Close()
	var err error
	s, err = Open(path, "pool", "source", "broker")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c.Store = s
	if err = c.Flush(context.Background()); err == nil {
		t.Fatal("expected sink outage")
	}
	queue, _ := s.Undelivered(0)
	if len(queue) != 1 || queue[0].AttributedRevenueWei != "13" {
		t.Fatalf("outbox %+v", queue)
	}
	out.fail = false
	if err = c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if remote.calls != 2 || len(out.items) != 1 {
		t.Fatalf("calls=%d deliveries=%d", remote.calls, len(out.items))
	}
	next := &pb.SpendAuthorization{Payload: &pb.SpendAuthorizationPayload{WholesaleAccountId: "test-account", AuthorizationId: "next", PredecessorAuthorizationId: "job", SettlementDomainId: "source", Payer: make([]byte, 20)}}
	raw, _ := proto.Marshal(next)
	if _, err = c.AdmitAuthorization(context.Background(), payment.AdmitAuthorizationRequest{AuthorizationBytes: raw}); err != nil {
		t.Fatal(err)
	}
	if a, err := s.Binding("next"); err != nil || a.Member != "member" {
		t.Fatalf("successor binding %+v %v", a, err)
	}
}
