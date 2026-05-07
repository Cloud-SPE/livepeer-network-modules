package encoder

import "sync"

// ringBuffer keeps at most cap bytes from the most recent writes.
// Used to bound captured FFmpeg stderr without unbounded heap growth
// over a long encode.
type ringBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func newRingBuffer(cap int) *ringBuffer {
	if cap <= 0 {
		cap = 128 * 1024
	}
	return &ringBuffer{cap: cap}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.cap {
		r.buf = r.buf[len(r.buf)-r.cap:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}
