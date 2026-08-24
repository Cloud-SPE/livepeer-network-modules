package store

import (
	"path/filepath"
	"testing"
)

// Rotation must be conditional on the predecessor the caller expected.
//
// Rotation has concurrent callers by nature: several payments can reach
// an exhausted rand at once and each will try to retire it. A caller
// that read the session BEFORE someone else rotated holds a stale
// work_id, and an unconditional reset would retire the SUCCESSOR that
// other caller just created — destroying a live identity and leaving the
// route with none.
//
// The compare-and-swap is what makes a second attempt a no-op instead of
// a second rotation.
func TestRotationIsConditionalOnTheExpectedPredecessor(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "rx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	key := TicketSessionKey{
		Sender:     []byte("sender-20-bytes!!!!!"),
		Recipient:  []byte("recipient-20-bytes!!"),
		Capability: "openai:chat-completions",
		Offering:   "default",
	}
	open := func(workID, rand string) {
		t.Helper()
		if _, _, err := st.GetOrCreateTicketSession(key, Session{
			WorkID:              workID,
			RecipientRand:       rand,
			PricePerWorkUnitWei: PricingUnset,
			FaceValueWei:        "1000",
			WinProb:             "1",
		}); err != nil {
			t.Fatalf("open %s: %v", workID, err)
		}
	}

	open("work-A", "11111")
	// First rotation retires A.
	rotated, err := st.ResetTicketSessionIfCurrent(key, "work-A")
	if err != nil {
		t.Fatal(err)
	}
	if !rotated {
		t.Fatal("rotating the live session reported no rotation")
	}
	// A successor takes the tuple.
	open("work-B", "22222")

	// A STALE caller — one that read the session before the rotation —
	// tries to retire its predecessor. This must be a no-op.
	rotatedAgain, err := st.ResetTicketSessionIfCurrent(key, "work-A")
	if err != nil {
		t.Fatal(err)
	}
	if rotatedAgain {
		t.Fatal("a stale caller rotated again; two callers at the boundary would each " +
			"retire the other's successor")
	}
	// And B must still be the live session, not collateral damage.
	live, err := st.TicketSessionFor(key)
	if err != nil {
		t.Fatal(err)
	}
	if live == nil {
		t.Fatal("the tuple has no live session; the stale caller retired the successor")
	}
	if live.WorkID != "work-B" {
		t.Fatalf("live session is %q; want work-B", live.WorkID)
	}
	if live.Closed {
		t.Fatal("the successor was closed by a stale caller's rotation")
	}
}
