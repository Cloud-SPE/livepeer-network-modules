package sessionrunner

import (
	"context"
	"log"
	"time"
)

// Watchdog enforces the stall-timeout: if the runner's IPC layer has
// not invoked Touch within the configured window, the runner is
// considered hung and is killed. The driver's teardown path observes
// the resulting Exited closure and finishes the cleanup.
type Watchdog struct {
	r        *Runner
	stall    time.Duration
	interval time.Duration
}

// NewWatchdog returns a Watchdog bound to a Runner.
func NewWatchdog(r *Runner, stall time.Duration) *Watchdog {
	if stall <= 0 {
		stall = 30 * time.Second
	}
	interval := stall / 5
	if interval < time.Second {
		interval = time.Second
	}
	return &Watchdog{
		r:        r,
		stall:    stall,
		interval: interval,
	}
}

// Run blocks until ctx is canceled or the runner exits. On stall,
// SIGKILLs the subprocess and returns.
func (w *Watchdog) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.r.exited:
			return
		case now := <-t.C:
			last := w.r.LastTouch()
			if last.IsZero() {
				continue
			}
			if now.Sub(last) > w.stall {
				log.Printf("session-runner: session=%s stall %s exceeded, killing", w.r.SessionID(), w.stall)
				_ = w.r.Kill()
				return
			}
		}
	}
}
