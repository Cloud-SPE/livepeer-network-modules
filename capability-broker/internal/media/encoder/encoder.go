// Package encoder is the broker's per-session FFmpeg subprocess
// wrapper. It accepts a Job (preset + scratch dir + I/O) and runs an
// `ffmpeg` child until the input pipe closes or the context cancels.
//
// Subprocess isolation is bare `exec.Command`: no broker-side cgroups,
// no container-per-stream. Per-host fairness is enforced at the
// operator deployment layer (Docker / Kubernetes limits). Cancellation
// is the SIGTERM-grace-SIGKILL sequence common to all the broker's
// subprocess wrappers.
package encoder

import (
	"context"
	"io"
	"sync/atomic"
)

// Encoder is the testable surface; SystemEncoder is the production
// exec.Cmd-backed implementation. Mode drivers depend on this
// interface so unit tests can inject a fake.
type Encoder interface {
	// Run starts the encoder and blocks until the input pipe closes
	// (graceful) or ctx is canceled (SIGTERM-then-SIGKILL).
	Run(ctx context.Context, j Job) error
	// Progress returns the running progress counter. Safe for
	// concurrent reads while Run is in flight.
	Progress() *Progress
}

// Job describes one encode invocation.
type Job struct {
	// Input is the FLV byte stream the encoder reads from
	// (typically the RTMP listener's pipe).
	Input io.Reader
	// ScratchDir is the per-session directory where the LL-HLS
	// playlist and segments land.
	ScratchDir string
	// Profile is the named encoder profile from presets.go.
	Profile string
	// Args is the fully-resolved FFmpeg argv (without the binary).
	// Profile resolution happens in the caller; the encoder treats
	// Args as opaque.
	Args []string
}

// Progress is the live work-unit counter. Both fields are populated
// by the stderr parser running in its own goroutine while the
// subprocess is alive. Width / Height are immutable from session-open
// (set by the profile resolver) and used by the
// `frame_megapixel` extractor variant.
type Progress struct {
	// FrameCount mirrors FFmpeg's `frame=N` line.
	FrameCount atomic.Uint64
	// OutTimeUs mirrors FFmpeg's `out_time_us=N` line.
	OutTimeUs atomic.Uint64
	// Width is the encoded width.
	Width uint64
	// Height is the encoded height.
	Height uint64
}

// CurrentUnits returns the running OutTime in seconds (ceiled). Used
// by the `out_time_seconds` LiveCounter sibling — the default for
// live RTMP per the work-unit table.
func (p *Progress) CurrentUnits() uint64 {
	if p == nil {
		return 0
	}
	us := p.OutTimeUs.Load()
	if us == 0 {
		return 0
	}
	return (us + 999_999) / 1_000_000
}

// CurrentFrames returns the running frame count.
func (p *Progress) CurrentFrames() uint64 {
	if p == nil {
		return 0
	}
	return p.FrameCount.Load()
}

// CurrentFrameMegapixels returns frames × width × height / 1e6.
func (p *Progress) CurrentFrameMegapixels() uint64 {
	if p == nil || p.Width == 0 || p.Height == 0 {
		return 0
	}
	frames := p.FrameCount.Load()
	return (frames * p.Width * p.Height) / 1_000_000
}
