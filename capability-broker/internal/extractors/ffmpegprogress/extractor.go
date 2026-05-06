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
// In v0.1 the broker doesn't yet drive an FFmpeg subprocess (rtmp-ingress
// session-open phase only — see plan 0011). This extractor is exercised
// in conformance via fixtures that put a literal progress block in the
// backend response body.
package ffmpegprogress

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

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
