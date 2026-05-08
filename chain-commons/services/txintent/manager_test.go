package txintent

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/config"
	cerrors "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/errors"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store"
)

type contextKey string

func newManager(t *testing.T) *Manager {
	t.Helper()
	st := store.Memory()
	m, err := New(
		config.Default().TxIntent,
		st,
		clock.System(),
		nil, // no logger needed for these tests
		metrics.NoOp(),
		nil, // no processor — drive transitions manually
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func sampleParams(kind string, key []byte) Params {
	return Params{
		Kind:      kind,
		KeyParams: key,
		To:        chain.Address{0x01},
		CallData:  []byte{0xde, 0xad, 0xbe, 0xef},
		Value:     big.NewInt(0),
		GasLimit:  500_000,
	}
}

func TestComputeID_Deterministic(t *testing.T) {
	a := ComputeID("RewardWithHint", []byte{1, 2, 3})
	b := ComputeID("RewardWithHint", []byte{1, 2, 3})
	if a != b {
		t.Errorf("ComputeID should be deterministic")
	}
}

func TestComputeID_DistinguishesKindFromKeyParams(t *testing.T) {
	// Without the separator, "AB" + nil and "A" + "B" would collide.
	a := ComputeID("AB", nil)
	b := ComputeID("A", []byte{'B'})
	if a == b {
		t.Errorf("ComputeID should distinguish (kind, params) pairs that concat to same bytes")
	}
}

func TestSubmit_NewIntent(t *testing.T) {
	m := newManager(t)
	id, err := m.Submit(context.Background(), sampleParams("Test", []byte{1}))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	got, err := m.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.Status != StatusPending {
		t.Errorf("Status = %s, want pending", got.Status)
	}
	if got.Kind != "Test" {
		t.Errorf("Kind = %q, want Test", got.Kind)
	}
}

func TestSubmit_RejectsZeroGasLimit(t *testing.T) {
	m := newManager(t)
	p := sampleParams("Test", nil)
	p.GasLimit = 0
	if _, err := m.Submit(context.Background(), p); err == nil {
		t.Errorf("Submit should reject GasLimit=0")
	}
}

func TestSubmit_RejectsEmptyKind(t *testing.T) {
	m := newManager(t)
	p := sampleParams("", nil)
	if _, err := m.Submit(context.Background(), p); err == nil {
		t.Errorf("Submit should reject empty Kind")
	}
}

func TestSubmit_Idempotent(t *testing.T) {
	m := newManager(t)
	p := sampleParams("Test", []byte{42})
	id1, err := m.Submit(context.Background(), p)
	if err != nil {
		t.Fatalf("Submit 1: %v", err)
	}
	id2, err := m.Submit(context.Background(), p)
	if err != nil {
		t.Fatalf("Submit 2: %v", err)
	}
	if id1 != id2 {
		t.Errorf("idempotent submits returned different IDs: %s vs %s", id1, id2)
	}
}

func TestSubmit_DistinctKeyParamsDifferentIDs(t *testing.T) {
	m := newManager(t)
	id1, _ := m.Submit(context.Background(), sampleParams("Test", []byte{1}))
	id2, _ := m.Submit(context.Background(), sampleParams("Test", []byte{2}))
	if id1 == id2 {
		t.Errorf("distinct KeyParams should produce distinct IDs")
	}
}

func TestStatus_NotFound(t *testing.T) {
	m := newManager(t)
	_, err := m.Status(context.Background(), IntentID{})
	if err != store.ErrNotFound {
		t.Errorf("Status of unknown ID = %v, want ErrNotFound", err)
	}
}

func TestSetStatus_Transitions(t *testing.T) {
	m := newManager(t)
	id, _ := m.Submit(context.Background(), sampleParams("Test", nil))

	for _, want := range []IntentStatus{StatusSigned, StatusSubmitted, StatusMined} {
		if err := m.SetStatus(id, want); err != nil {
			t.Fatalf("SetStatus(%s): %v", want, err)
		}
		got, _ := m.Status(context.Background(), id)
		if got.Status != want {
			t.Errorf("Status after SetStatus(%s) = %s", want, got.Status)
		}
	}
}

func TestSetStatus_RejectsAfterTerminal(t *testing.T) {
	m := newManager(t)
	id, _ := m.Submit(context.Background(), sampleParams("Test", nil))
	// MarkConfirmed requires StatusMined or StatusSubmitted; the call before
	// SetStatus is intentional to exercise the rejection path. Result is
	// discarded; we only care about the post-SetStatus assertion below.
	_, _ = m.MarkConfirmed(id), m.SetStatus(id, StatusSubmitted)
	if err := m.MarkConfirmed(id); err != nil {
		t.Fatalf("MarkConfirmed: %v", err)
	}
	if err := m.SetStatus(id, StatusPending); err == nil {
		t.Errorf("SetStatus on terminal intent should fail")
	}
}

func TestMarkConfirmed_FromMined(t *testing.T) {
	m := newManager(t)
	id, _ := m.Submit(context.Background(), sampleParams("Test", nil))
	_ = m.SetStatus(id, StatusSubmitted)
	_ = m.SetStatus(id, StatusMined)
	if err := m.MarkConfirmed(id); err != nil {
		t.Fatalf("MarkConfirmed: %v", err)
	}
	got, _ := m.Status(context.Background(), id)
	if got.Status != StatusConfirmed {
		t.Errorf("Status = %s, want confirmed", got.Status)
	}
	if got.ConfirmedAt == nil {
		t.Errorf("ConfirmedAt should be set")
	}
}

func TestMarkConfirmed_RejectsFromPending(t *testing.T) {
	m := newManager(t)
	id, _ := m.Submit(context.Background(), sampleParams("Test", nil))
	if err := m.MarkConfirmed(id); err == nil {
		t.Errorf("MarkConfirmed from pending should fail")
	}
}

func TestMarkFailed_RejectsAfterTerminal(t *testing.T) {
	m := newManager(t)
	id, _ := m.Submit(context.Background(), sampleParams("Test", nil))
	_ = m.SetStatus(id, StatusSubmitted)
	if err := m.MarkConfirmed(id); err != nil {
		t.Fatalf("MarkConfirmed: %v", err)
	}
	reason := cerrors.New(cerrors.ClassReverted, "tx.reverted", "test")
	if err := m.MarkFailed(id, reason); err == nil {
		t.Errorf("MarkFailed on terminal intent should fail")
	}
}

func TestAppendAttempt_TracksReplacements(t *testing.T) {
	m := newManager(t)
	id, _ := m.Submit(context.Background(), sampleParams("Test", nil))

	// First attempt: nonce 0
	a1 := IntentAttempt{
		Nonce:         0,
		GasFeeCap:     big.NewInt(1000),
		GasTipCap:     big.NewInt(100),
		SignedTxHash:  chain.TxHash{0xaa},
		BroadcastedAt: time.Now(),
	}
	if err := m.AppendAttempt(id, a1, StatusSubmitted); err != nil {
		t.Fatalf("AppendAttempt 1: %v", err)
	}

	// Replacement at same nonce with bumped fee
	a2 := IntentAttempt{
		Nonce:         0,
		GasFeeCap:     big.NewInt(1110),
		GasTipCap:     big.NewInt(110),
		SignedTxHash:  chain.TxHash{0xbb},
		BroadcastedAt: time.Now(),
	}
	if err := m.AppendAttempt(id, a2, StatusSubmitted); err != nil {
		t.Fatalf("AppendAttempt 2: %v", err)
	}

	got, _ := m.Status(context.Background(), id)
	if len(got.Attempts) != 2 {
		t.Fatalf("Attempts len = %d, want 2", len(got.Attempts))
	}
	if got.Attempts[0].ReplacedAt == nil {
		t.Errorf("first attempt should be marked replaced")
	}
	if got.Attempts[1].ReplacedAt != nil {
		t.Errorf("second (current) attempt should not be replaced")
	}
	if cur := got.CurrentAttempt(); cur == nil || cur.SignedTxHash != (chain.TxHash{0xbb}) {
		t.Errorf("CurrentAttempt should be the second attempt")
	}
}

func TestList_FiltersByKind(t *testing.T) {
	m := newManager(t)
	_, _ = m.Submit(context.Background(), sampleParams("A", []byte{1}))
	_, _ = m.Submit(context.Background(), sampleParams("B", []byte{1}))
	_, _ = m.Submit(context.Background(), sampleParams("A", []byte{2}))

	out, err := m.List(context.Background(), Filter{Kinds: []string{"A"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("List by Kind=A returned %d intents, want 2", len(out))
	}
	for _, intent := range out {
		if intent.Kind != "A" {
			t.Errorf("got Kind=%q in filtered list", intent.Kind)
		}
	}
}

func TestList_FiltersByStatus(t *testing.T) {
	m := newManager(t)
	id1, _ := m.Submit(context.Background(), sampleParams("Test", []byte{1}))
	id2, _ := m.Submit(context.Background(), sampleParams("Test", []byte{2}))
	_ = m.SetStatus(id1, StatusSubmitted)
	_ = id2 // remains pending

	out, _ := m.List(context.Background(), Filter{Statuses: []IntentStatus{StatusSubmitted}})
	if len(out) != 1 {
		t.Errorf("expected 1 submitted, got %d", len(out))
	}
	out, _ = m.List(context.Background(), Filter{Statuses: []IntentStatus{StatusPending}})
	if len(out) != 1 {
		t.Errorf("expected 1 pending, got %d", len(out))
	}
}

func TestWait_ReturnsImmediatelyIfTerminal(t *testing.T) {
	m := newManager(t)
	id, _ := m.Submit(context.Background(), sampleParams("Test", nil))
	_ = m.SetStatus(id, StatusSubmitted)
	_ = m.MarkConfirmed(id)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	got, err := m.Wait(ctx, id)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got.Status != StatusConfirmed {
		t.Errorf("Status = %s, want confirmed", got.Status)
	}
}

func TestWait_BlocksUntilTransition(t *testing.T) {
	m := newManager(t)
	id, _ := m.Submit(context.Background(), sampleParams("Test", nil))

	done := make(chan TxIntent, 1)
	errCh := make(chan error, 1)
	go func() {
		t, err := m.Wait(context.Background(), id)
		if err != nil {
			errCh <- err
			return
		}
		done <- t
	}()

	// Briefly let the goroutine register as a waiter.
	time.Sleep(10 * time.Millisecond)
	_ = m.SetStatus(id, StatusSubmitted)
	_ = m.MarkConfirmed(id)

	select {
	case got := <-done:
		if got.Status != StatusConfirmed {
			t.Errorf("Wait returned status %s, want confirmed", got.Status)
		}
	case err := <-errCh:
		t.Fatalf("Wait err: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after transition")
	}
}

func TestWait_CtxCancel(t *testing.T) {
	m := newManager(t)
	id, _ := m.Submit(context.Background(), sampleParams("Test", nil))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err := m.Wait(ctx, id)
	if err != context.Canceled {
		t.Errorf("Wait returned %v, want context.Canceled", err)
	}
}

func TestWait_NotFound(t *testing.T) {
	m := newManager(t)
	_, err := m.Wait(context.Background(), IntentID{0xff})
	if err != store.ErrNotFound {
		t.Errorf("Wait of unknown ID = %v, want ErrNotFound", err)
	}
}

func TestPersistence_RoundTripMsgpack(t *testing.T) {
	m := newManager(t)
	id, _ := m.Submit(context.Background(), Params{
		Kind:      "Persisted",
		KeyParams: []byte("key"),
		To:        chain.Address{0x12, 0x34},
		CallData:  []byte("calldata"),
		Value:     big.NewInt(123456),
		GasLimit:  100_000,
		Metadata:  map[string]string{"round": "42"},
	})
	got, err := m.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.Kind != "Persisted" {
		t.Errorf("Kind = %q", got.Kind)
	}
	if got.Value.Int64() != 123456 {
		t.Errorf("Value = %s", got.Value)
	}
	if got.Metadata["round"] != "42" {
		t.Errorf("Metadata.round = %q", got.Metadata["round"])
	}
	if got.GasLimit != 100_000 {
		t.Errorf("GasLimit = %d", got.GasLimit)
	}
}

func TestResume_NoProcessorIsNoOp(t *testing.T) {
	m := newManager(t)
	_, _ = m.Submit(context.Background(), sampleParams("Test", nil))
	if err := m.Resume(context.Background()); err != nil {
		t.Errorf("Resume with nil processor: %v", err)
	}
}

func TestResume_DispatchesNonTerminal(t *testing.T) {
	st := store.Memory()
	calls := make(chan IntentID, 10)
	proc := processorFunc(func(ctx context.Context, m *Manager, id IntentID) {
		calls <- id
	})

	m, err := New(config.Default().TxIntent, st, clock.System(), nil, metrics.NoOp(), proc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	id1, _ := m.Submit(context.Background(), sampleParams("A", []byte{1}))
	id2, _ := m.Submit(context.Background(), sampleParams("B", []byte{1}))

	// Drain Submit-time process calls.
	drain := func() {
		for {
			select {
			case <-calls:
			case <-time.After(100 * time.Millisecond):
				return
			}
		}
	}
	drain()

	// Confirm one; only the other should resume.
	_ = m.SetStatus(id1, StatusSubmitted)
	_ = m.MarkConfirmed(id1)

	if err := m.Resume(context.Background()); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	got := map[IntentID]bool{}
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case id := <-calls:
			got[id] = true
		case <-timeout:
			if got[id1] {
				t.Errorf("Resume should NOT dispatch terminal intent %s", id1)
			}
			if !got[id2] {
				t.Errorf("Resume should dispatch non-terminal intent %s", id2)
			}
			return
		}
	}
}

func TestSubmit_DetachesProcessorFromCallerCancellation(t *testing.T) {
	st := store.Memory()
	calls := make(chan context.Context, 1)
	proc := processorFunc(func(ctx context.Context, _ *Manager, _ IntentID) {
		calls <- ctx
	})

	m, err := New(config.Default().TxIntent, st, clock.System(), nil, metrics.NoOp(), proc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey("request-id"), "abc123"))
	cancel()
	if _, err := m.Submit(ctx, sampleParams("Test", []byte{9})); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	select {
	case procCtx := <-calls:
		if procCtx.Done() != nil {
			t.Fatal("processor context should be detached from caller cancellation")
		}
		if procCtx.Err() != nil {
			t.Fatalf("processor context err = %v, want nil", procCtx.Err())
		}
		if got := procCtx.Value(contextKey("request-id")); got != "abc123" {
			t.Fatalf("processor context value = %v, want preserved request-id", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("processor was not dispatched")
	}
}

type processorFunc func(ctx context.Context, m *Manager, id IntentID)

func (f processorFunc) Process(ctx context.Context, m *Manager, id IntentID) { f(ctx, m, id) }

func TestNotifyWaiters_OnFailedTerminal(t *testing.T) {
	m := newManager(t)
	id, _ := m.Submit(context.Background(), sampleParams("Test", nil))

	done := make(chan TxIntent, 1)
	go func() {
		t, _ := m.Wait(context.Background(), id)
		done <- t
	}()

	time.Sleep(10 * time.Millisecond)
	reason := cerrors.New(cerrors.ClassReverted, "tx.reverted", "rejected")
	_ = m.MarkFailed(id, reason)

	select {
	case got := <-done:
		if got.Status != StatusFailed {
			t.Errorf("Wait returned status %s, want failed", got.Status)
		}
		if got.FailedReason == nil || got.FailedReason.Code != "tx.reverted" {
			t.Errorf("FailedReason = %+v", got.FailedReason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after MarkFailed")
	}
}
