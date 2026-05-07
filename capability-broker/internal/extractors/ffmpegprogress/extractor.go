// Package ffmpegprogress implements the ffmpeg-progress extractor per
// livepeer-network-protocol/extractors/ffmpeg-progress.md.
//
// Parses FFmpeg `-progress pipe:1` output (key=value lines) from the
// response body and returns a count per the configured `unit`:
//
//   - "frame"             — final value of `frame=N`
//   - "frame_megapixel"   — frame × width × height / 1_000_000 (floored)
//   - "out_time_seconds"  — out_time_us / 1_000_000, ceiled
//
// The mode driver wires a LiveCounter sibling that reads the same
// counters mid-flight so the interim-debit ticker in plan 0015 sees a
// running view of the encoded media seconds / frames.
package ffmpegprogress

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

const Name = "ffmpeg-progress"

type Extractor struct {
	unit   string
	width  uint64
	height uint64
}

var _ extractors.Extractor = (*Extractor)(nil)

func New(cfg map[string]any) (extractors.Extractor, error) {
	unit, ok := cfg["unit"].(string)
	if !ok || unit == "" {
		return nil, fmt.Errorf("ffmpeg-progress: unit is required")
	}
	switch unit {
	case "frame", "frame_megapixel", "out_time_seconds":
	default:
		return nil, fmt.Errorf("ffmpeg-progress: unit must be 'frame' | 'frame_megapixel' | 'out_time_seconds' (got %q)", unit)
	}
	var width, height uint64
	if unit == "frame_megapixel" {
		w, err := readPositiveUint(cfg, "width")
		if err != nil {
			return nil, fmt.Errorf("ffmpeg-progress: width is required when unit=frame_megapixel: %w", err)
		}
		h, err := readPositiveUint(cfg, "height")
		if err != nil {
			return nil, fmt.Errorf("ffmpeg-progress: height is required when unit=frame_megapixel: %w", err)
		}
		width, height = w, h
	}
	return &Extractor{unit: unit, width: width, height: height}, nil
}

func (e *Extractor) Name() string { return Name }

func (e *Extractor) Extract(ctx context.Context, req *extractors.Request, resp *extractors.Response) (uint64, error) {
	progress := parseProgress(string(resp.Body))
	switch e.unit {
	case "frame":
		return progress.frame, nil
	case "frame_megapixel":
		return (progress.frame * e.width * e.height) / 1_000_000, nil
	case "out_time_seconds":
		return uint64(math.Ceil(float64(progress.outTimeUs) / 1_000_000)), nil
	}
	return 0, nil
}

type progressData struct {
	frame     uint64
	outTimeUs uint64
}

// parseProgress scans key=value lines per FFmpeg's -progress output.
// Unrecognized keys are ignored. Final values win for repeated keys.
func parseProgress(s string) progressData {
	var p progressData
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		switch key {
		case "frame":
			if n, err := strconv.ParseUint(val, 10, 64); err == nil {
				p.frame = n
			}
		case "out_time_us":
			if n, err := strconv.ParseUint(val, 10, 64); err == nil {
				p.outTimeUs = n
			}
		}
	}
	return p
}

// Unit returns the configured unit name. Used by the mode driver to
// decide which LiveCounter shape to wire.
func (e *Extractor) Unit() string { return e.unit }

// Width returns the configured frame width (zero unless
// unit=frame_megapixel).
func (e *Extractor) Width() uint64 { return e.width }

// Height returns the configured frame height (zero unless
// unit=frame_megapixel).
func (e *Extractor) Height() uint64 { return e.height }

// LiveCounter is the running view exposed to the interim-debit
// ticker. The mode driver registers a LiveCounter that wraps the
// encoder's per-session FrameCount / OutTimeUs atomics; this package
// provides the conversion to the configured unit.
type LiveCounter struct {
	frame     *atomic.Uint64
	outTimeUs *atomic.Uint64
	unit      string
	width     uint64
	height    uint64
}

var _ extractors.LiveCounter = (*LiveCounter)(nil)

// NewLiveCounter wires the running counter to the encoder's atomics.
// frame and outTimeUs MUST point to the same atomics the encoder's
// stderr parser writes; nil values yield a counter that returns 0.
func (e *Extractor) NewLiveCounter(frame, outTimeUs *atomic.Uint64) *LiveCounter {
	return &LiveCounter{
		frame:     frame,
		outTimeUs: outTimeUs,
		unit:      e.unit,
		width:     e.width,
		height:    e.height,
	}
}

// CurrentUnits implements extractors.LiveCounter.
func (lc *LiveCounter) CurrentUnits() uint64 {
	if lc == nil {
		return 0
	}
	switch lc.unit {
	case "frame":
		if lc.frame == nil {
			return 0
		}
		return lc.frame.Load()
	case "frame_megapixel":
		if lc.frame == nil {
			return 0
		}
		return (lc.frame.Load() * lc.width * lc.height) / 1_000_000
	case "out_time_seconds":
		if lc.outTimeUs == nil {
			return 0
		}
		us := lc.outTimeUs.Load()
		if us == 0 {
			return 0
		}
		return (us + 999_999) / 1_000_000
	}
	return 0
}

func readPositiveUint(cfg map[string]any, key string) (uint64, error) {
	v, ok := cfg[key]
	if !ok {
		return 0, fmt.Errorf("missing")
	}
	switch n := v.(type) {
	case int:
		if n <= 0 {
			return 0, fmt.Errorf("must be > 0")
		}
		return uint64(n), nil
	case float64:
		if n <= 0 {
			return 0, fmt.Errorf("must be > 0")
		}
		return uint64(n), nil
	default:
		return 0, fmt.Errorf("must be a positive number (got %T)", v)
	}
}
