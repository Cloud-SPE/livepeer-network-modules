package repo

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStateRepoSaveRunPrunesHistory(t *testing.T) {
	repo, err := Open(filepath.Join(t.TempDir(), "state.db"), 2)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer repo.Close()

	for _, id := range []string{"001", "002", "003"} {
		if err := repo.SaveRun(RunRecord{
			RunID:       id,
			StartedAt:   time.Unix(0, 1).UTC(),
			CompletedAt: time.Unix(0, 2).UTC(),
		}); err != nil {
			t.Fatalf("SaveRun(%s) error = %v", id, err)
		}
	}

	runs, err := repo.ListRuns(10)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("len(ListRuns()) = %d, want 2", len(runs))
	}
	if runs[0].RunID != "003" || runs[1].RunID != "002" {
		t.Fatalf("ListRuns() = %+v", runs)
	}
}

func TestStateRepoUpsertIntentTracksRetryMetadata(t *testing.T) {
	repo, err := Open(filepath.Join(t.TempDir(), "state.db"), 10)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer repo.Close()

	at := time.Unix(1700000000, 0).UTC()
	if err := repo.UpsertIntent(IntentUpdate{
		IntentID:        "payout-1",
		Phase:           "dispatch",
		Status:          "error",
		Error:           "rpc timeout",
		DispatchAttempt: true,
		Failed:          true,
	}, at); err != nil {
		t.Fatalf("UpsertIntent(dispatch) error = %v", err)
	}
	if err := repo.UpsertIntent(IntentUpdate{
		IntentID:     "payout-1",
		Phase:        "confirm",
		Status:       "paid",
		TxHash:       "0xabc123",
		ConfirmCheck: true,
		Succeeded:    true,
	}, at.Add(time.Minute)); err != nil {
		t.Fatalf("UpsertIntent(confirm) error = %v", err)
	}

	intents, err := repo.ListIntents(10)
	if err != nil {
		t.Fatalf("ListIntents() error = %v", err)
	}
	if len(intents) != 1 {
		t.Fatalf("len(ListIntents()) = %d, want 1", len(intents))
	}
	rec := intents[0]
	if rec.DispatchAttempts != 1 || rec.ConfirmChecks != 1 || rec.FailureCount != 1 {
		t.Fatalf("intent retry metadata = %+v", rec)
	}
	if rec.LastStatus != "paid" || rec.LastTxHash != "0xabc123" || rec.LastError != "" {
		t.Fatalf("intent final state = %+v", rec)
	}
	if rec.FirstSeenAt.IsZero() || rec.LastSucceededAt.IsZero() || rec.LastFailedAt.IsZero() {
		t.Fatalf("intent timestamps = %+v", rec)
	}

	got, found, err := repo.GetIntent("payout-1")
	if err != nil {
		t.Fatalf("GetIntent() error = %v", err)
	}
	if !found || got.IntentID != "payout-1" {
		t.Fatalf("GetIntent() = %+v, %v", got, found)
	}
}
