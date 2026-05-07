// Package sessionrunner owns the per-session workload-specific
// subprocess that the session-control-plus-media driver supervises.
//
// One container (or process — debug-only `process` runtime) per
// session, lifetime-bound to the session, broker-owned. The broker is
// opaque to the runner's workload semantics; envelopes flow through
// the gRPC unix-socket IPC defined in
// livepeer-network-protocol/proto/livepeer/sessionrunner/v1.
package sessionrunner

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Config is the per-broker configuration for the session-runner
// supervisor — the operator-tunable knobs from §10.1.
type Config struct {
	// ContainerRuntime is one of "docker" | "process". v0.1 supports
	// docker as the production target; process is debug-only and
	// shells out to the configured Command directly.
	ContainerRuntime string

	// SocketDir is the directory under which per-session unix
	// sockets are minted as `sess_${session_id}.sock`. The
	// directory must exist and be writable by the broker.
	SocketDir string

	// StartupTimeout caps the time from subprocess launch to a
	// successful Health() probe.
	StartupTimeout time.Duration

	// StallTimeout is the watchdog window: no IPC traffic for this
	// long triggers a kill.
	StallTimeout time.Duration

	// ShutdownGrace is the window the runner gets to drain on
	// graceful shutdown before the broker SIGKILLs.
	ShutdownGrace time.Duration

	// ExtraCaps are the Linux capabilities to opt back in (Q7
	// lock — drop-all default).
	ExtraCaps []string
}

// DefaultConfig returns the recommended defaults from §10.1.
func DefaultConfig() Config {
	return Config{
		ContainerRuntime: "docker",
		SocketDir:        "/var/run/livepeer/session-runner",
		StartupTimeout:   30 * time.Second,
		StallTimeout:     30 * time.Second,
		ShutdownGrace:    5 * time.Second,
	}
}

// CapabilityBackend is the per-capability runner declaration the
// host-config carries. Mirrors §10.2's `session_runner` block.
type CapabilityBackend struct {
	Image          string
	Command        []string
	Env            map[string]string
	MemoryLimit    string
	CPULimit       string
	GPUs           int
	NetworkMode    string
	StartupTimeout time.Duration
}

// Supervisor is the per-broker gateway between the
// session-control-plus-media driver and a per-session container
// runtime. v0.1 supports the docker runtime in-tree; the process
// runtime is debug-only and used by the conformance stub image.
type Supervisor struct {
	cfg Config
}

// NewSupervisor returns a supervisor bound to a broker-wide config.
// Returns an error if the configured runtime is unknown.
func NewSupervisor(cfg Config) (*Supervisor, error) {
	if cfg.ContainerRuntime == "" {
		cfg.ContainerRuntime = "docker"
	}
	switch cfg.ContainerRuntime {
	case "docker", "process":
	default:
		return nil, fmt.Errorf("session-runner: unknown container-runtime %q", cfg.ContainerRuntime)
	}
	if cfg.StartupTimeout <= 0 {
		cfg.StartupTimeout = 30 * time.Second
	}
	if cfg.StallTimeout <= 0 {
		cfg.StallTimeout = 30 * time.Second
	}
	if cfg.ShutdownGrace <= 0 {
		cfg.ShutdownGrace = 5 * time.Second
	}
	if cfg.SocketDir == "" {
		cfg.SocketDir = "/var/run/livepeer/session-runner"
	}
	return &Supervisor{cfg: cfg}, nil
}

// Config returns the supervisor's effective configuration (test helper).
func (s *Supervisor) Config() Config { return s.cfg }

// SocketPathFor returns the per-session socket path the broker mints
// for sessionID.
func (s *Supervisor) SocketPathFor(sessionID string) string {
	return filepath.Join(s.cfg.SocketDir, "sess_"+sanitize(sessionID)+".sock")
}

// Launch starts a subprocess for sessionID. The returned Runner
// exposes the per-session lifecycle: Health probe, IPC channels,
// graceful Shutdown, hard Kill, watchdog Touch.
func (s *Supervisor) Launch(ctx context.Context, sessionID string, backend CapabilityBackend) (*Runner, error) {
	if backend.Image == "" && s.cfg.ContainerRuntime == "docker" {
		return nil, errors.New("session-runner: capability backend image is required for docker runtime")
	}
	if len(backend.Command) == 0 && s.cfg.ContainerRuntime == "process" {
		return nil, errors.New("session-runner: capability backend command is required for process runtime")
	}
	socket := s.SocketPathFor(sessionID)

	startupTimeout := backend.StartupTimeout
	if startupTimeout <= 0 {
		startupTimeout = s.cfg.StartupTimeout
	}

	r := &Runner{
		supervisor:     s,
		sessionID:      sessionID,
		socketPath:     socket,
		startupTimeout: startupTimeout,
		startedAt:      time.Now(),
	}
	r.lastTouch.Store(r.startedAt.UnixNano())

	cmd := s.buildCmd(ctx, sessionID, socket, backend)
	r.cmd = cmd

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("session-runner: start: %w", err)
	}
	r.exited = make(chan struct{})
	go func() {
		err := cmd.Wait()
		r.mu.Lock()
		r.waitErr = err
		r.mu.Unlock()
		close(r.exited)
	}()
	return r, nil
}

// buildCmd renders the exec.Cmd per the configured runtime. Docker
// invocations apply the Q7 cap-drop posture and pass through the
// capability backend's image / env / resource requests.
func (s *Supervisor) buildCmd(ctx context.Context, sessionID, socket string, b CapabilityBackend) *exec.Cmd {
	if s.cfg.ContainerRuntime == "process" {
		cmd := exec.CommandContext(ctx, b.Command[0], b.Command[1:]...)
		cmd.Env = append(cmd.Environ(), "LIVEPEER_SESSION_RUNNER_SOCK="+socket)
		for k, v := range b.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		return cmd
	}
	args := []string{
		"run", "--rm",
		"--name", "livepeer-session-runner-" + sanitize(sessionID),
		"--cap-drop=ALL",
		"-v", filepath.Dir(socket) + ":" + filepath.Dir(socket),
		"-e", "LIVEPEER_SESSION_RUNNER_SOCK=" + socket,
	}
	for _, c := range s.cfg.ExtraCaps {
		args = append(args, "--cap-add="+c)
	}
	for k, v := range b.Env {
		args = append(args, "-e", k+"="+v)
	}
	if b.MemoryLimit != "" {
		args = append(args, "--memory="+b.MemoryLimit)
	}
	if b.CPULimit != "" {
		args = append(args, "--cpus="+b.CPULimit)
	}
	if b.GPUs > 0 {
		args = append(args, "--gpus", fmt.Sprintf("%d", b.GPUs))
	}
	if b.NetworkMode != "" {
		args = append(args, "--network="+b.NetworkMode)
	}
	args = append(args, b.Image)
	args = append(args, b.Command...)
	return exec.CommandContext(ctx, "docker", args...)
}

// sanitize strips characters that would break a unix-socket path or
// docker container name. Session IDs are crypto/rand hex prefixed
// with `sess_`, so this is a defense-in-depth pass.
func sanitize(s string) string {
	out := strings.Builder{}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			out.WriteRune(r)
		case r == '_' || r == '-':
			out.WriteRune(r)
		}
	}
	return out.String()
}

// Runner is the per-session handle the driver retains. Construct via
// Supervisor.Launch. Goroutine-safe: Health/Shutdown/Touch are all
// safe to call concurrently with the watchdog goroutine.
type Runner struct {
	supervisor *Supervisor
	sessionID  string
	socketPath string

	startupTimeout time.Duration
	startedAt      time.Time

	cmd     *exec.Cmd
	exited  chan struct{}
	mu      sync.Mutex
	waitErr error

	lastTouch  atomic.Int64
	closing    atomic.Bool
}

// SessionID returns the runner's session id.
func (r *Runner) SessionID() string { return r.sessionID }

// SocketPath returns the runner's IPC unix-socket path.
func (r *Runner) SocketPath() string { return r.socketPath }

// Exited returns a channel that closes when the subprocess exits.
func (r *Runner) Exited() <-chan struct{} { return r.exited }

// WaitErr returns the subprocess wait() error after Exited fires.
// Returns nil before then.
func (r *Runner) WaitErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.waitErr
}

// Touch updates the watchdog timestamp; the IPC layer calls it on
// every successful frame.
func (r *Runner) Touch() {
	r.lastTouch.Store(time.Now().UnixNano())
}

// LastTouch reports the most recent IPC timestamp.
func (r *Runner) LastTouch() time.Time {
	return time.Unix(0, r.lastTouch.Load())
}

// Health waits for the runner to declare itself "ready" via a
// successful Health() RPC, or returns the configured startup-timeout
// error. The caller-supplied probe is the actual gRPC client (so this
// package stays free of generated proto symbols).
func (r *Runner) Health(ctx context.Context, probe func(context.Context) error) error {
	deadline := r.startedAt.Add(r.startupTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.exited:
			return errors.New("session-runner: subprocess exited before Health()")
		case <-ticker.C:
			if time.Now().After(deadline) {
				return errors.New("session-runner: startup timeout exceeded")
			}
			if err := probe(ctx); err == nil {
				return nil
			}
		}
	}
}

// Shutdown signals the runner to drain. The caller-supplied stop
// closure is the gRPC Shutdown() RPC; failure of that RPC falls
// through to a hard kill after ShutdownGrace.
func (r *Runner) Shutdown(ctx context.Context, stop func(context.Context) error) error {
	if !r.closing.CompareAndSwap(false, true) {
		return nil
	}
	graceCtx, cancel := context.WithTimeout(ctx, r.supervisor.cfg.ShutdownGrace)
	defer cancel()
	if stop != nil {
		_ = stop(graceCtx)
	}
	select {
	case <-r.exited:
		return nil
	case <-graceCtx.Done():
	}
	return r.Kill()
}

// Kill sends SIGKILL to the subprocess. Idempotent.
func (r *Runner) Kill() error {
	if r.cmd == nil || r.cmd.Process == nil {
		return nil
	}
	_ = r.cmd.Process.Kill()
	select {
	case <-r.exited:
	case <-time.After(time.Second):
	}
	return nil
}
