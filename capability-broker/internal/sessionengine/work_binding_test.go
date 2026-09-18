package sessionengine

import (
	"context"
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/workledger"
	"math/big"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionPersistsWorkBindingBeforeExecution(t *testing.T) {
	h := newHarness(t)
	calls := 0
	h.engine.cfg.BindWork = func(workID, requestID string, spec *OfferingSpec) error {
		calls++
		if workID == "" || requestID == "" || spec != h.spec || h.runner.created != 0 {
			t.Fatal("binding not established before runner execution")
		}
		return nil
	}
	h.open(t)
	if calls != 1 || h.runner.created != 1 {
		t.Fatalf("bindings=%d creates=%d", calls, h.runner.created)
	}
}
func TestSessionBindingFailureStopsExecution(t *testing.T) {
	h := newHarness(t)
	bound := false
	h.engine.cfg.BindWork = func(string, string, *OfferingSpec) error {
		bound = true
		return fmt.Errorf("durable attribution unavailable")
	}
	_, err := h.engine.Open(context.Background(), OpenRequest{RequestID: "request", GatewaySessionID: "gateway", AuthorizationBytes: h.authorization(t, "auth", "request", "gateway", 100), InitialReservationWei: h.spec.PricePerWorkUnitWei, Spec: h.spec, CapacityRef: "slot"})
	if !bound || err == nil || h.runner.created != 0 {
		t.Fatalf("unattributed session ran: %v creates=%d", err, h.runner.created)
	}
}

func TestRegionalSessionBilledDeltasSurviveEventReplayAndFinalSettlement(t *testing.T) {
	h := newHarness(t)
	ledger, err := workledger.Open(filepath.Join(t.TempDir(), "work.db"), "pool", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "broker")
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	h.engine.cfg.Payment = &workledger.Client{Client: h.pay, AccountClient: h.pay, Store: ledger}
	h.engine.cfg.BindWork = func(auth, request string, spec *OfferingSpec) error {
		return ledger.Bind(auth, workledger.Attribution{Member: "member", Backend: spec.BackendRef, Enrollment: "host", Capability: spec.Capability, Offering: spec.Offering, RequestID: request})
	}
	opened := h.open(t)
	ctx := context.Background()
	for _, ev := range []Event{usageEvent("one", 1, 5), usageEvent("two", 2, 12), usageEvent("two", 2, 12)} {
		if _, err = h.engine.ProcessEvent(ctx, opened.SessionID, ev); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = h.engine.End(ctx, opened.SessionID, "client_end"); err != nil {
		t.Fatal(err)
	}
	if err = ledger.Finalize(100, time.Now()); err != nil {
		t.Fatal(err)
	}
	receipts, err := ledger.Undelivered(0)
	if err != nil || len(receipts) != 2 {
		t.Fatalf("receipts %+v %v", receipts, err)
	}
	total := new(big.Int)
	units := uint64(0)
	for _, r := range receipts {
		n, _ := new(big.Int).SetString(r.AttributedRevenueWei, 10)
		total.Add(total, n)
		units += r.ActualUnits
	}
	if total.Int64() != 120 || units != 12 {
		t.Fatalf("billing=%s units=%d", total, units)
	}
}
