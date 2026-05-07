package rtmp

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// PipeSink is a Sink implementation that bridges the RTMP handler
// (write side) and an io.Reader consumer (read side). The encoder
// wrapper in internal/media/encoder reads from PipeSink.Reader and
// hands it to FFmpeg's stdin.
//
// Closing the writer half (via Close) propagates EOF to the reader so
// the FFmpeg subprocess exits cleanly when the publisher disconnects.
type PipeSink struct {
	r        *io.PipeReader
	w        *io.PipeWriter
	bytesIn  atomic.Uint64
	closed   atomic.Bool
	mu       sync.Mutex
	onTouch  func(time.Time)
	closedAt time.Time
}

// NewPipeSink returns a Sink that streams written FLV bytes through a
// pipe, with an optional onTouch hook fired on every write.
func NewPipeSink(onTouch func(time.Time)) *PipeSink {
	pr, pw := io.Pipe()
	return &PipeSink{r: pr, w: pw, onTouch: onTouch}
}

// Reader returns the read side of the pipe. The caller (encoder
// wrapper) hands this to FFmpeg's stdin.
func (s *PipeSink) Reader() io.Reader { return s.r }

// WriteFLV implements Sink.
func (s *PipeSink) WriteFLV(p []byte) (int, error) {
	if s.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	n, err := s.w.Write(p)
	s.bytesIn.Add(uint64(n))
	return n, err
}

// Close implements Sink.
func (s *PipeSink) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	s.mu.Lock()
	s.closedAt = time.Now()
	s.mu.Unlock()
	_ = s.w.Close()
	_ = s.r.Close()
	return nil
}

// Touch implements Sink.
func (s *PipeSink) Touch(now time.Time) {
	if s.onTouch != nil {
		s.onTouch(now)
	}
}

// BytesWritten returns the running count of FLV bytes the RTMP handler
// has written into the pipe. Used by the metering hot path.
func (s *PipeSink) BytesWritten() uint64 { return s.bytesIn.Load() }

// DiscardSink is a Sink that drops writes (used by the C1 smoke and
// some unit tests).
type DiscardSink struct {
	bytesIn atomic.Uint64
}

// NewDiscardSink returns a Sink that drops every write.
func NewDiscardSink() *DiscardSink { return &DiscardSink{} }

// WriteFLV implements Sink.
func (s *DiscardSink) WriteFLV(p []byte) (int, error) {
	s.bytesIn.Add(uint64(len(p)))
	return len(p), nil
}

// Close implements Sink.
func (s *DiscardSink) Close() error { return nil }

// Touch implements Sink.
func (s *DiscardSink) Touch(_ time.Time) {}

// BytesWritten returns the running count of FLV bytes received.
func (s *DiscardSink) BytesWritten() uint64 { return s.bytesIn.Load() }
