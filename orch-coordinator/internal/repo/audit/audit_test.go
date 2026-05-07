package audit

import (
	"path/filepath"
	"testing"
)

func TestLog_AppendAndRecent(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for i := 0; i < 5; i++ {
		if _, err := l.Append(Event{Outcome: OutcomeAccepted}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := l.Recent(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	if got[0].Seq != 4 || got[2].Seq != 2 {
		t.Fatalf("seq order: %+v", got)
	}
}

func TestLog_OutcomeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := l.Append(Event{Outcome: OutcomeRollbackRejected, ErrorCode: "rollback_rejected"}); err != nil {
		t.Fatal(err)
	}
	got, err := l.Recent(1)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Outcome != OutcomeRollbackRejected {
		t.Fatalf("outcome: %s", got[0].Outcome)
	}
}
