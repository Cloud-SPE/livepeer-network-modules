package sessioncontrolplusmedia

import (
	"sync"
)

// replayBuffer is a bounded ring of server-emitted envelopes retained
// for replay-on-reconnect per plan 0012-followup §4.2.1. Each entry
// carries the envelope and its serialized JSON byte length.
//
// Goroutine-safe: the relay goroutine appends; the reconnect handler
// drains under read.
type replayBuffer struct {
	mu       sync.Mutex
	maxMsgs  int
	maxBytes int
	totalLen int
	entries  []replayEntry
}

type replayEntry struct {
	seq     uint64
	payload []byte
}

func newReplayBuffer(maxMsgs, maxBytes int) *replayBuffer {
	if maxMsgs <= 0 {
		maxMsgs = 64
	}
	return &replayBuffer{
		maxMsgs:  maxMsgs,
		maxBytes: maxBytes,
	}
}

// Append records a server-emitted envelope. The oldest entries are
// dropped when either the message-count or byte cap is exceeded.
func (r *replayBuffer) Append(seq uint64, payload []byte) {
	if r == nil {
		return
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, replayEntry{seq: seq, payload: cp})
	r.totalLen += len(cp)
	r.shrink()
}

// shrink drops oldest entries until the count + byte caps hold.
// Caller holds r.mu.
func (r *replayBuffer) shrink() {
	for len(r.entries) > r.maxMsgs {
		r.totalLen -= len(r.entries[0].payload)
		r.entries = r.entries[1:]
	}
	if r.maxBytes > 0 {
		for r.totalLen > r.maxBytes && len(r.entries) > 0 {
			r.totalLen -= len(r.entries[0].payload)
			r.entries = r.entries[1:]
		}
	}
}

// Since returns a copy of the buffered entries with seq strictly
// greater than lastSeq. Returns nil when nothing is pending.
func (r *replayBuffer) Since(lastSeq uint64) []replayEntry {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) == 0 {
		return nil
	}
	out := make([]replayEntry, 0, len(r.entries))
	for _, e := range r.entries {
		if e.seq > lastSeq {
			out = append(out, replayEntry{seq: e.seq, payload: append([]byte(nil), e.payload...)})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Len reports the current count of buffered entries (test helper).
func (r *replayBuffer) Len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// Bytes reports the current total payload byte size (test helper).
func (r *replayBuffer) Bytes() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.totalLen
}
