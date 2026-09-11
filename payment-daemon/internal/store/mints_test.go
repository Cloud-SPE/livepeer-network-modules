package store

import (
	"path/filepath"
	"testing"
)

// A sender's nonce must survive its own restart.
//
// The receiver's replay ledger is durable, so a sender whose nonce was
// not restarts at 1 and replays nonces already consumed: every ticket
// rejected, nothing credited, and the broker serving work out of balance
// credited before the restart until it runs dry. The gateway sees
// success the whole time. Found on the pilot stack after a routine
// restart, which is the only place it could be found — a single process
// never replays itself.
func TestSenderNonceSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payer.db")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var issued []uint32
	for i := 0; i < 3; i++ {
		n, err := st.NextSenderNonce("work-a")
		if err != nil {
			t.Fatal(err)
		}
		issued = append(issued, n)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// Restart against the same store, as a daemon does.
	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	for i := 0; i < 3; i++ {
		n, err := st2.NextSenderNonce("work-a")
		if err != nil {
			t.Fatal(err)
		}
		issued = append(issued, n)
	}

	seen := map[uint32]bool{}
	for _, n := range issued {
		if seen[n] {
			t.Fatalf("nonce %d issued twice across a restart (%v); the receiver rejects the "+
				"second as a replay and credits nothing", n, issued)
		}
		seen[n] = true
	}
	for i := 1; i < len(issued); i++ {
		if issued[i] <= issued[i-1] {
			t.Fatalf("nonces not monotonic across a restart: %v", issued)
		}
	}
}

// Separate work_ids have separate streams: a nonce is only meaningful
// against the recipient rand it was minted for.
func TestSenderNoncePerWorkID(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "payer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	a, err := st.NextSenderNonce("work-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.NextSenderNonce("work-b")
	if err != nil {
		t.Fatal(err)
	}
	if a != 1 || b != 1 {
		t.Fatalf("streams are not independent: work-a=%d work-b=%d", a, b)
	}
}
