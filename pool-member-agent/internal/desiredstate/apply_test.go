package desiredstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubRunner records what Apply asked the host to do and can fail at
// any one stage, which is how the report's detail is checked.
type stubRunner struct {
	calls       []string
	written     string
	writtenPath string
	failWrite   error
	failPull    error
	failUp      error
}

func (s *stubRunner) WriteCompose(path, content string) error {
	s.calls = append(s.calls, "write")
	s.writtenPath, s.written = path, content
	return s.failWrite
}

func (s *stubRunner) Pull(ctx context.Context, path string) error {
	s.calls = append(s.calls, "pull")
	return s.failPull
}

func (s *stubRunner) Up(ctx context.Context, path string) error {
	s.calls = append(s.calls, "up")
	return s.failUp
}

func twoServiceDoc() Document {
	return Document{
		EnrollmentID: "host-1",
		Revision:     "rev-7",
		// Out of name order: the compose file's order is the renderer's.
		Services: []Service{
			{Name: "runner-b", ComposeFragment: "  runner-b:\n    image: b\n", AssignmentID: "unit-b|chat-b"},
			{Name: "runner-a", ComposeFragment: "  runner-a:\n    image: a", AssignmentID: "unit-a|chat-a"},
		},
	}
}

// One compose file, one `services:` key, every service under it —
// including the draining one, which the pool wants gone but which is
// still finishing work.
func TestRenderComposeEmitsEveryServiceSortedUnderOneServicesKey(t *testing.T) {
	doc := twoServiceDoc()
	doc.Services = append(doc.Services, Service{
		Name: "runner-c", ComposeFragment: "  runner-c:\n    image: c\n", Draining: true,
	})
	out := RenderCompose(doc)

	if got := strings.Count(out, "\nservices:\n"); got != 1 {
		t.Fatalf("compose file has %d services: keys, want exactly 1:\n%s", got, out)
	}
	for _, name := range []string{"runner-a", "runner-b", "runner-c"} {
		if !strings.Contains(out, "  "+name+":\n") {
			t.Fatalf("compose file is missing %s (a draining service still has to run):\n%s", name, out)
		}
	}
	a, b, c := strings.Index(out, "runner-a:"), strings.Index(out, "runner-b:"), strings.Index(out, "runner-c:")
	if !(a < b && b < c) {
		t.Fatalf("services are not sorted by name (a=%d b=%d c=%d):\n%s", a, b, c, out)
	}
	// A fragment that did not end in a newline must not swallow the
	// next service's first line.
	if !strings.Contains(out, "    image: a\n  runner-b:") {
		t.Fatalf("a fragment without a trailing newline ran into the next service:\n%s", out)
	}
	if !strings.Contains(out, doc.Revision) {
		t.Fatalf("compose file does not name the revision it came from:\n%s", out)
	}
}

// The happy path: every service reported honestly, and a draining one
// reported as finished so the pool can retire the placement.
func TestApplyReportsRunningAndMarksTheDrainingService(t *testing.T) {
	doc := twoServiceDoc()
	doc.Services = append(doc.Services, Service{
		Name: "runner-c", ComposeFragment: "  runner-c:\n", Draining: true,
	})
	runner := &stubRunner{}
	report := Apply(context.Background(), runner, "/tmp/does-not-matter/runners.compose.yaml", doc)

	if strings.Join(runner.calls, ",") != "write,pull,up" {
		t.Fatalf("runner calls = %v, want the compose file written, then pulled, then started", runner.calls)
	}
	if runner.writtenPath != "/tmp/does-not-matter/runners.compose.yaml" {
		t.Fatalf("compose written to %q", runner.writtenPath)
	}
	if runner.written != RenderCompose(doc) {
		t.Fatalf("what was written is not what the document renders to:\n%s", runner.written)
	}
	if report.Revision != doc.Revision {
		t.Fatalf("report revision = %q, want %q", report.Revision, doc.Revision)
	}
	got := map[string]ServiceStatus{}
	for _, service := range report.Services {
		got[service.Name] = service
	}
	if len(got) != 3 {
		t.Fatalf("report = %+v, want one entry per service", report.Services)
	}
	for _, name := range []string{"runner-a", "runner-b"} {
		if got[name].Status != StatusRunning || got[name].Detail != "" {
			t.Fatalf("%s = %+v, want %s", name, got[name], StatusRunning)
		}
	}
	// Reporting a draining service as running would keep its assignment
	// alive forever; the pool retires it on this report.
	if got["runner-c"].Status != StatusStopped || got["runner-c"].Detail != "draining" {
		t.Fatalf("draining service = %+v, want %s with a draining detail", got["runner-c"], StatusStopped)
	}
}

// A host that could not comply must say so for every service, and the
// detail has to name the stage: an image that will not download is a
// different problem from one that will not start.
func TestApplyReportsEveryServiceFailedWithTheStageThatFailed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		fail       func(*stubRunner)
		wantCalls  string
		wantDetail string
		notDetail  string
	}{
		{
			name:       "write",
			fail:       func(s *stubRunner) { s.failWrite = errors.New("read-only file system") },
			wantCalls:  "write",
			wantDetail: "write compose: read-only file system",
		},
		{
			name:       "pull",
			fail:       func(s *stubRunner) { s.failPull = errors.New("manifest unknown") },
			wantCalls:  "write,pull",
			wantDetail: "pull: manifest unknown",
			notDetail:  "up",
		},
		{
			name:       "up",
			fail:       func(s *stubRunner) { s.failUp = errors.New("port is already allocated") },
			wantCalls:  "write,pull,up",
			wantDetail: "up: port is already allocated",
			notDetail:  "pull:",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := twoServiceDoc()
			runner := &stubRunner{}
			tc.fail(runner)
			report := Apply(context.Background(), runner, "runners.compose.yaml", doc)

			if strings.Join(runner.calls, ",") != tc.wantCalls {
				t.Fatalf("runner calls = %v, want %s (a failed stage stops the next one)", runner.calls, tc.wantCalls)
			}
			if report.Revision != doc.Revision {
				t.Fatalf("report revision = %q, want %q", report.Revision, doc.Revision)
			}
			if len(report.Services) != len(doc.Services) {
				t.Fatalf("report = %+v, want every service accounted for", report.Services)
			}
			for _, service := range report.Services {
				if service.Status != StatusFailed {
					t.Fatalf("%s = %s, want %s: the pool's next decision depends on knowing this host failed",
						service.Name, service.Status, StatusFailed)
				}
				if !strings.Contains(service.Detail, tc.wantDetail) {
					t.Fatalf("%s detail = %q, want it to name the stage (%q)", service.Name, service.Detail, tc.wantDetail)
				}
				if tc.notDetail != "" && strings.Contains(service.Detail, tc.notDetail) {
					t.Fatalf("%s detail = %q blames the wrong stage (%q)", service.Name, service.Detail, tc.notDetail)
				}
			}
		})
	}
}

// The compose file is read by a docker that may be running while it is
// rewritten. A partially written file is a host that stops serving for
// reasons nobody can reconstruct, so the replacement has to be atomic.
func TestComposeRunnerWriteComposeReplacesTheFileAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "runners.compose.yaml")
	runner := ComposeRunner{}

	first := "services:\n  runner-a:\n    image: a\n"
	if err := runner.WriteCompose(path, first); err != nil {
		t.Fatalf("WriteCompose() error = %v", err)
	}

	// A hard link holds on to the file that is there now. If the second
	// write truncated and rewrote that same file in place, the link
	// would see the new content — and docker reading it mid-write would
	// see half a file.
	link := filepath.Join(dir, "nested", "observer.yaml")
	if err := os.Link(path, link); err != nil {
		t.Fatalf("Link() error = %v", err)
	}

	second := "services:\n" + strings.Repeat("  runner-b:\n    image: b\n", 4096)
	if err := runner.WriteCompose(path, second); err != nil {
		t.Fatalf("second WriteCompose() error = %v", err)
	}

	observed, err := os.ReadFile(link)
	if err != nil {
		t.Fatalf("ReadFile(link) error = %v", err)
	}
	if string(observed) != first {
		t.Fatal("the file docker already had open was rewritten in place; a concurrent read could see a partial compose file")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != second {
		t.Fatalf("compose file content is not what was written (%d of %d bytes)", len(got), len(second))
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("left %s behind; a stale temp file is a compose file nobody knows the age of", entry.Name())
		}
	}
}
