package ffmpegprogress

import (
	"sync/atomic"
	"testing"
)

func TestLiveCounter_Frame(t *testing.T) {
	ext, err := New(map[string]any{"type": "ffmpeg-progress", "unit": "frame"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e := ext.(*Extractor)
	var frame, outTime atomic.Uint64
	lc := e.NewLiveCounter(&frame, &outTime)

	if got := lc.CurrentUnits(); got != 0 {
		t.Errorf("zero: got=%d want=0", got)
	}
	frame.Store(123)
	if got := lc.CurrentUnits(); got != 123 {
		t.Errorf("frame=123: got=%d want=123", got)
	}
}

func TestLiveCounter_OutTimeSeconds(t *testing.T) {
	ext, err := New(map[string]any{"type": "ffmpeg-progress", "unit": "out_time_seconds"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e := ext.(*Extractor)
	var frame, outTime atomic.Uint64
	lc := e.NewLiveCounter(&frame, &outTime)

	outTime.Store(2_500_000)
	if got := lc.CurrentUnits(); got != 3 {
		t.Errorf("out_time_us=2_500_000 → CurrentUnits=%d want=3 (ceil)", got)
	}
}

func TestLiveCounter_FrameMegapixel(t *testing.T) {
	ext, err := New(map[string]any{
		"type":   "ffmpeg-progress",
		"unit":   "frame_megapixel",
		"width":  1920,
		"height": 1080,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e := ext.(*Extractor)
	var frame, outTime atomic.Uint64
	lc := e.NewLiveCounter(&frame, &outTime)

	frame.Store(100)
	want := uint64(100) * 1920 * 1080 / 1_000_000
	if got := lc.CurrentUnits(); got != want {
		t.Errorf("frame_megapixel @ 100 frames: got=%d want=%d", got, want)
	}
}

func TestLiveCounter_NilSafe(t *testing.T) {
	var lc *LiveCounter
	if got := lc.CurrentUnits(); got != 0 {
		t.Errorf("nil receiver: got=%d want=0", got)
	}

	ext, _ := New(map[string]any{"type": "ffmpeg-progress", "unit": "frame"})
	e := ext.(*Extractor)
	if got := e.NewLiveCounter(nil, nil).CurrentUnits(); got != 0 {
		t.Errorf("nil atomics: got=%d want=0", got)
	}
}
