package encoder

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// SystemEncoder runs FFmpeg via os/exec.
type SystemEncoder struct {
	Bin         string
	CancelGrace time.Duration
	MaxLogBytes int

	progress Progress
	mu       sync.Mutex
	stderr   string
}

// Compile-time interface check.
var _ Encoder = (*SystemEncoder)(nil)

// NewSystemEncoder returns a SystemEncoder with sensible defaults. The
// caller normally fills in Bin from `--ffmpeg-binary` and CancelGrace
// from `--ffmpeg-cancel-grace`.
func NewSystemEncoder(bin string, grace time.Duration) *SystemEncoder {
	if bin == "" {
		bin = "ffmpeg"
	}
	if grace == 0 {
		grace = 5 * time.Second
	}
	return &SystemEncoder{
		Bin:         bin,
		CancelGrace: grace,
		MaxLogBytes: 128 * 1024,
	}
}

// Progress returns the live progress counter.
func (e *SystemEncoder) Progress() *Progress { return &e.progress }

// Stderr returns the captured stderr ring buffer.
func (e *SystemEncoder) Stderr() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stderr
}

// Run starts the encoder and blocks until the input pipe closes or
// ctx is canceled. Returns nil on graceful EOF; a wrapped error on
// subprocess failure.
func (e *SystemEncoder) Run(ctx context.Context, j Job) error {
	if j.Input == nil {
		return errors.New("encoder: nil Input reader")
	}
	if len(j.Args) == 0 {
		return errors.New("encoder: empty Args")
	}

	cmd := exec.CommandContext(ctx, e.Bin, j.Args...)
	cmd.Stdin = j.Input

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("encoder: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("encoder: start ffmpeg: %w", err)
	}
	log.Printf("encoder: ffmpeg started pid=%d profile=%s", cmd.Process.Pid, j.Profile)

	stderrBuf := newRingBuffer(e.MaxLogBytes)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = parseProgressStream(stderrPipe, &e.progress, stderrBuf)
	}()

	doneCh := make(chan error, 1)
	go func() { doneCh <- cmd.Wait() }()

	var waitErr error
	select {
	case <-ctx.Done():
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case waitErr = <-doneCh:
		case <-time.After(e.CancelGrace):
			_ = cmd.Process.Kill()
			waitErr = <-doneCh
		}
	case waitErr = <-doneCh:
	}
	wg.Wait()

	e.mu.Lock()
	e.stderr = stderrBuf.String()
	e.mu.Unlock()

	if waitErr == nil {
		return nil
	}
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		return fmt.Errorf("encoder: ffmpeg exited code=%d: %s", exitErr.ExitCode(), trimLog(stderrBuf.String()))
	}
	return waitErr
}

// trimLog reduces a captured-stderr blob to a one-line summary.
func trimLog(s string) string {
	if len(s) > 256 {
		s = s[len(s)-256:]
	}
	return s
}
