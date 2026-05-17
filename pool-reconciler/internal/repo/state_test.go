package repo

import (
	"path/filepath"
	"testing"
)

func TestRoundStateLifecycle(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = r.Close() }()

	if err := r.MarkAttempt(124); err != nil {
		t.Fatalf("MarkAttempt() error = %v", err)
	}
	rec, found, err := r.GetRound(124)
	if err != nil {
		t.Fatalf("GetRound() error = %v", err)
	}
	if !found || rec.Attempts != 1 || rec.Status != "attempted" {
		t.Fatalf("unexpected round record: %+v found=%v", rec, found)
	}

	if err := r.MarkFailed(124, "boom"); err != nil {
		t.Fatalf("MarkFailed() error = %v", err)
	}
	rec, found, err = r.GetRound(124)
	if err != nil {
		t.Fatalf("GetRound() error = %v", err)
	}
	if !found || rec.Status != "failed" || rec.LastError != "boom" {
		t.Fatalf("unexpected failed round record: %+v", rec)
	}

	if err := r.MarkClosed(124); err != nil {
		t.Fatalf("MarkClosed() error = %v", err)
	}
	rec, found, err = r.GetRound(124)
	if err != nil {
		t.Fatalf("GetRound() error = %v", err)
	}
	if !found || rec.Status != "closed" || rec.LastError != "" {
		t.Fatalf("unexpected closed round record: %+v", rec)
	}
}

func TestListPendingRounds(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = r.Close() }()

	if err := r.MarkAttempt(2); err != nil {
		t.Fatalf("MarkAttempt(2) error = %v", err)
	}
	if err := r.MarkFailed(3, "boom"); err != nil {
		t.Fatalf("MarkFailed(3) error = %v", err)
	}
	if err := r.MarkAttempt(4); err != nil {
		t.Fatalf("MarkAttempt(4) error = %v", err)
	}
	if err := r.MarkClosed(4); err != nil {
		t.Fatalf("MarkClosed(4) error = %v", err)
	}

	pending, err := r.ListPendingRounds(10)
	if err != nil {
		t.Fatalf("ListPendingRounds() error = %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("ListPendingRounds() len = %d, want 2", len(pending))
	}
	if pending[0].RoundID != 2 || pending[1].RoundID != 3 {
		t.Fatalf("pending rounds = %+v", pending)
	}
}
