package devclock

import (
	"bytes"
	"testing"
)

func TestDevClockProgressAndCopies(t *testing.T) {
	c := New()
	if c.LastInitializedRound() != 1 || c.LastSeenL1Block().Int64() != 1 || c.GetTranscoderPoolSize().Int64() != 100 {
		t.Fatalf("unexpected initial clock state")
	}
	h := c.LastInitializedL1BlockHash()
	h[0] ^= 0xff
	if bytes.Equal(h, c.LastInitializedL1BlockHash()) {
		t.Fatal("block hash was not copied")
	}
	b := c.LastSeenL1Block()
	b.SetInt64(999)
	if c.LastSeenL1Block().Int64() != 1 {
		t.Fatal("block number was not copied")
	}
	c.Tick(98)
	if c.LastInitializedRound() != 1 || c.LastSeenL1Block().Int64() != 99 {
		t.Fatalf("premature round transition")
	}
	c.Tick(1)
	if c.LastInitializedRound() != 2 || c.LastSeenL1Block().Int64() != 100 {
		t.Fatalf("round boundary not observed")
	}
	if got := c.AdvanceRounds(0); got != 2 {
		t.Fatalf("zero advance=%d", got)
	}
	if got := c.AdvanceRounds(3); got != 5 || c.LastSeenL1Block().Int64() != 400 {
		t.Fatalf("advance result=%d block=%s", got, c.LastSeenL1Block())
	}
}
