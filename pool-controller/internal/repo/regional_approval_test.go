package repo

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/payoutpolicy"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	bolt "go.etcd.io/bbolt"
)

func readyApproval(t *testing.T, st *StateRepo, sourceStart int64) types.PayoutBatch {
	t.Helper()
	sources, err := st.RevenueSources(nil)
	if err != nil || len(sources) == 0 {
		t.Fatalf("sources %v", err)
	}
	for n := sourceStart; n < sourceStart+14; n++ {
		freezeWindowRound(t, st, sources[0].Source, n, "10", "1", "1")
	}
	_, batch, err := st.CloseRegionalWindow(uint64(sourceStart))
	if err != nil {
		t.Fatal(err)
	}
	return batch
}
func TestRegionalApprovalRollsBackAndReplayPreservesExecutorState(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	windowSource(t, st)
	batch := readyApproval(t, st, 100)
	first := fmt.Sprintf("payout-%s-%04d", batch.ID, 0)
	second := fmt.Sprintf("payout-%s-%04d", batch.ID, 1)
	// Conflict on the second insert forces a rollback after the first insert.
	if err := st.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(payoutIntentsBucket)).Put([]byte(second), []byte(`{}`))
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveRegionalBatch(batch.ID, "operator"); err == nil {
		t.Fatal("conflicting partial approval accepted")
	}
	if _, err := st.GetPayoutIntent(first); err == nil {
		t.Fatal("first intent escaped rolled back transaction")
	}
	unchanged, _ := st.GetPayoutBatch(batch.ID)
	events, _ := st.ListAuditEvents()
	if unchanged.Status != types.PayoutBatchPendingApproval || len(events) != 0 {
		t.Fatal("partial approval published")
	}
	if err := st.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte(payoutIntentsBucket)).Delete([]byte(second)) }); err != nil {
		t.Fatal(err)
	}
	approved, err := st.ApproveRegionalBatch(batch.ID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	intent, err := st.GetPayoutIntent(first)
	if err != nil {
		t.Fatal(err)
	}
	if intent.PoolID != st.PoolID() || intent.PayoutBatchID != batch.ID || intent.AmountWei != "63" || intent.ChainID != 42161 {
		t.Fatalf("intent %+v", intent)
	}
	stale := intent
	intent.Status = "leased"
	intent.LeaseID = "lease"
	intent.LeaseOwner = "executor"
	intent.LeasedAt = time.Now().UTC()
	intent.LeaseExpiresAt = intent.LeasedAt.Add(time.Minute)
	if err := st.SavePayoutIntent(intent); err != nil {
		t.Fatal(err)
	}
	stale.Status = "leased"
	stale.LeaseID = "competing"
	if err := st.SavePayoutIntent(stale); err == nil {
		t.Fatal("stale concurrent lease overwrote winner")
	}
	intent, _ = st.GetPayoutIntent(first)
	bad := intent
	bad.AmountWei = "1"
	if err := st.SavePayoutIntent(bad); err == nil {
		t.Fatal("leased amount rewritten")
	}
	intent.Status = "paid"
	intent.PaidAt = time.Now().UTC()
	intent.TxHash = "confirmed-transaction"
	if err := st.SavePayoutIntent(intent); err != nil {
		t.Fatal(err)
	}
	paid, _ := st.GetPayoutIntent(first)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	replay, err := st.ApproveRegionalBatch(batch.ID, "retry-operator")
	after, _ := st.GetPayoutIntent(first)
	if err != nil || !sameJSON(approved, replay) || !sameJSON(paid, after) {
		t.Fatalf("approval replay reset accounting %v", err)
	}
	events, _ = st.ListAuditEvents()
	if len(events) != 1 {
		t.Fatalf("duplicate approval audit %d", len(events))
	}
}
func TestRegionalPolicyApprovalSerializesDailyBoundsWithoutScaleHold(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	windowSource(t, st)
	one := readyApproval(t, st, 100)
	two := readyApproval(t, st, 114)
	policy := RegionalApprovalPolicy{Hash: "policy-hash", Policy: payoutpolicy.Policy{AutoApprove: payoutpolicy.AutoApprove{Enabled: true, MaxBatchWei: "200", MaxPerMemberWei: "100", RequireScaleGTE: 1, MaxBatchesPerDay: 1}}}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{one.ID, two.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := st.ApproveRegionalBatchWithPolicy(id, policy)
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("daily bound allowed %d approvals", successes)
	}
	intents, err := st.ListPayoutIntents(0)
	if err != nil || len(intents) != 2 {
		t.Fatalf("partial or duplicated export %d %v", len(intents), err)
	}
}

func TestZeroWorkApprovalCreatesNoTransfers(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	source := windowSource(t, st)
	for n := int64(100); n < 114; n++ {
		freezeWindowRound(t, st, source, n, "10")
	}
	_, batch, err := st.CloseRegionalWindow(100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveRegionalBatch(batch.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	intents, err := st.ListPayoutIntents(0)
	if err != nil || len(intents) != 0 {
		t.Fatalf("zero-work transfer created %d %v", len(intents), err)
	}
}
