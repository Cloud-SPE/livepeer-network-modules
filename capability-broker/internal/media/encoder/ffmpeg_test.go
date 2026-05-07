package encoder

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// shortRunBin returns a binary that exits cleanly without arguments.
// Used to validate the wrapper's start/wait/cleanup paths without
// requiring ffmpeg on the test host.
func shortRunBin() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "/bin/true"
}

func TestSystemEncoder_RequiresInput(t *testing.T) {
	enc := NewSystemEncoder("/bin/true", time.Second)
	err := enc.Run(context.Background(), Job{Args: []string{"-version"}})
	if err == nil || !strings.Contains(err.Error(), "nil Input reader") {
		t.Fatalf("Run nil input: err=%v", err)
	}
}

func TestSystemEncoder_RequiresArgs(t *testing.T) {
	enc := NewSystemEncoder("/bin/true", time.Second)
	err := enc.Run(context.Background(), Job{Input: strings.NewReader("")})
	if err == nil || !strings.Contains(err.Error(), "empty Args") {
		t.Fatalf("Run empty args: err=%v", err)
	}
}

func TestSystemEncoder_CleanExit(t *testing.T) {
	enc := NewSystemEncoder(shortRunBin(), time.Second)
	err := enc.Run(context.Background(), Job{
		Input: strings.NewReader(""),
		Args:  []string{"-version"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestSystemEncoder_ContextCancel(t *testing.T) {
	if _, err := lookupBin("sleep"); err != nil {
		t.Skipf("sleep not available: %v", err)
	}
	enc := NewSystemEncoder("sleep", 200*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := enc.Run(ctx, Job{
		Input: strings.NewReader(""),
		Args:  []string{"5"},
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("Run did not error after cancel")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("cancel took too long: %v", elapsed)
	}
}

func lookupBin(name string) (string, error) {
	enc := NewSystemEncoder(name, time.Second)
	return enc.Bin, nil
}
