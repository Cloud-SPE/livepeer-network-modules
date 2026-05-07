package sessionrunner

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewSupervisorDefaults(t *testing.T) {
	t.Parallel()
	s, err := NewSupervisor(Config{})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if s.Config().ContainerRuntime != "docker" {
		t.Fatalf("default runtime: got %q, want docker", s.Config().ContainerRuntime)
	}
	if s.Config().StartupTimeout != 30*time.Second {
		t.Fatalf("default StartupTimeout: got %s, want 30s", s.Config().StartupTimeout)
	}
}

func TestNewSupervisorRejectsUnknownRuntime(t *testing.T) {
	t.Parallel()
	if _, err := NewSupervisor(Config{ContainerRuntime: "rkt"}); err == nil {
		t.Fatal("expected error on unknown runtime")
	}
}

func TestSocketPathFor(t *testing.T) {
	t.Parallel()
	s, _ := NewSupervisor(Config{SocketDir: "/tmp/runner"})
	got := s.SocketPathFor("sess_abc")
	want := filepath.Join("/tmp/runner", "sess_sess_abc.sock")
	if got != want {
		t.Fatalf("SocketPathFor: got %q, want %q", got, want)
	}
}

func TestSanitizeStripsBadChars(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in, want string
	}{
		{"sess_abc", "sess_abc"},
		{"sess-123", "sess-123"},
		{"abc def", "abcdef"},
		{"abc/../def", "abcdef"},
		{"$cap", "cap"},
	} {
		if got := sanitize(tc.in); got != tc.want {
			t.Fatalf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLaunchProcessRuntime(t *testing.T) {
	t.Parallel()
	s, err := NewSupervisor(Config{
		ContainerRuntime: "process",
		SocketDir:        t.TempDir(),
		StartupTimeout:   2 * time.Second,
		ShutdownGrace:    500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r, err := s.Launch(ctx, "sess_proc", CapabilityBackend{
		Command: []string{"/bin/sleep", "60"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer r.Kill()

	if r.SessionID() != "sess_proc" {
		t.Fatalf("SessionID: %q", r.SessionID())
	}
	if !strings.Contains(r.SocketPath(), "sess_") {
		t.Fatalf("SocketPath: %q", r.SocketPath())
	}

	r.Touch()
	if got := r.LastTouch(); time.Since(got) > time.Second {
		t.Fatalf("Touch did not update timestamp: %s", got)
	}

	if err := r.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	select {
	case <-r.Exited():
	case <-time.After(2 * time.Second):
		t.Fatal("subprocess did not exit after Kill")
	}
}

func TestLaunchProcessRuntimeRejectsEmptyCommand(t *testing.T) {
	t.Parallel()
	s, _ := NewSupervisor(Config{ContainerRuntime: "process", SocketDir: t.TempDir()})
	if _, err := s.Launch(context.Background(), "sess", CapabilityBackend{}); err == nil {
		t.Fatal("expected error on empty command")
	}
}

func TestLaunchDockerRequiresImage(t *testing.T) {
	t.Parallel()
	s, _ := NewSupervisor(Config{SocketDir: t.TempDir()})
	_, err := s.Launch(context.Background(), "sess", CapabilityBackend{Command: []string{"foo"}})
	if err == nil {
		t.Fatal("expected error on missing image")
	}
}

func TestHealthRespectsStartupTimeout(t *testing.T) {
	t.Parallel()
	s, _ := NewSupervisor(Config{
		ContainerRuntime: "process",
		SocketDir:        t.TempDir(),
		StartupTimeout:   100 * time.Millisecond,
	})
	r, err := s.Launch(context.Background(), "sess", CapabilityBackend{
		Command: []string{"/bin/sleep", "60"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer r.Kill()
	probeFails := func(_ context.Context) error { return errors.New("not ready") }
	start := time.Now()
	err = r.Health(context.Background(), probeFails)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Health blocked too long: %s", elapsed)
	}
}

func TestHealthErrorOnEarlyExit(t *testing.T) {
	t.Parallel()
	s, _ := NewSupervisor(Config{
		ContainerRuntime: "process",
		SocketDir:        t.TempDir(),
		StartupTimeout:   2 * time.Second,
	})
	r, err := s.Launch(context.Background(), "sess", CapabilityBackend{
		Command: []string{"/bin/true"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	probeNeverFires := func(_ context.Context) error { return errors.New("not ready") }
	start := time.Now()
	if err := r.Health(context.Background(), probeNeverFires); err == nil {
		t.Fatal("expected error after early exit")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("Health did not detect early exit promptly")
	}
}

func TestShutdownGraceFallthrough(t *testing.T) {
	t.Parallel()
	s, _ := NewSupervisor(Config{
		ContainerRuntime: "process",
		SocketDir:        t.TempDir(),
		StartupTimeout:   2 * time.Second,
		ShutdownGrace:    200 * time.Millisecond,
	})
	r, err := s.Launch(context.Background(), "sess", CapabilityBackend{
		Command: []string{"/bin/sleep", "60"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	stopFails := func(_ context.Context) error { return errors.New("rpc fail") }
	if err := r.Shutdown(context.Background(), stopFails); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case <-r.Exited():
	case <-time.After(2 * time.Second):
		t.Fatal("subprocess did not exit after Shutdown")
	}
	if err := r.Shutdown(context.Background(), stopFails); err != nil {
		t.Fatalf("Shutdown idempotent: %v", err)
	}
}

func TestWatchdogKillsOnStall(t *testing.T) {
	t.Parallel()
	s, _ := NewSupervisor(Config{
		ContainerRuntime: "process",
		SocketDir:        t.TempDir(),
		StartupTimeout:   2 * time.Second,
	})
	r, err := s.Launch(context.Background(), "sess", CapabilityBackend{
		Command: []string{"/bin/sleep", "60"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	r.Touch()
	w := NewWatchdog(r, 100*time.Millisecond)
	go w.Run(context.Background())
	select {
	case <-r.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("watchdog did not kill stalled runner")
	}
}
