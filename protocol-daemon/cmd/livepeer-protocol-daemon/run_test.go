package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunHelpOK(t *testing.T) {
	var buf bytes.Buffer
	code := run(context.Background(), []string{"--help"}, &buf)
	// Standard flag.ContinueOnError returns 2 on -h.
	if code != 2 {
		t.Fatalf("--help code = %d; want 2", code)
	}
	if !strings.Contains(buf.String(), "round-init") {
		t.Fatalf("expected --help output to mention round-init, got: %s", buf.String())
	}
}

func TestRunInvalidMode(t *testing.T) {
	var buf bytes.Buffer
	code := run(context.Background(), []string{"--mode=bogus", "--dev"}, &buf)
	if code != 2 {
		t.Fatalf("invalid mode code = %d; want 2 (config-validate fail)", code)
	}
}

func TestRunInvalidLogLevel(t *testing.T) {
	var buf bytes.Buffer
	code := run(context.Background(), []string{"--log-level=bogus", "--dev"}, &buf)
	if code != 2 {
		t.Fatalf("invalid log-level code = %d; want 2", code)
	}
}

func TestRunDevModeBootShutdown(t *testing.T) {
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	code := run(ctx, []string{"--mode=round-init", "--dev"}, &buf)
	if code != 0 {
		t.Fatalf("dev round-init code = %d; want 0; output: %s", code, buf.String())
	}
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV(" a, b ,,c, ")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("splitCSV = %#v", got)
	}
	if got := splitCSV(""); len(got) != 0 {
		t.Fatalf("splitCSV(empty) = %#v; want []", got)
	}
}

func TestBuildLoggerJSON(t *testing.T) {
	var buf bytes.Buffer
	l, err := buildLogger("info", "json", &buf)
	if err != nil {
		t.Fatal(err)
	}
	l.Info("hello", "k", "v")
	if !strings.Contains(buf.String(), `"hello"`) {
		t.Fatalf("json output missing message: %s", buf.String())
	}
}

func TestBuildLoggerText(t *testing.T) {
	var buf bytes.Buffer
	l, err := buildLogger("debug", "text", &buf)
	if err != nil {
		t.Fatal(err)
	}
	l.Debug("hello", "k", "v")
	if !strings.Contains(buf.String(), "hello") {
		t.Fatalf("text output missing: %s", buf.String())
	}
}

func TestBuildLoggerInvalid(t *testing.T) {
	if _, err := buildLogger("bogus", "text", &bytes.Buffer{}); err == nil {
		t.Fatal("expected level error")
	}
	if _, err := buildLogger("info", "bogus", &bytes.Buffer{}); err == nil {
		t.Fatal("expected format error")
	}
}

func TestSlogAdapter(t *testing.T) {
	var buf bytes.Buffer
	l, _ := buildLogger("debug", "text", &buf)
	a := newSlogLogger(l)
	a.Info("info msg", logField("k", "v"))
	a.Debug("debug msg")
	a.Warn("warn msg")
	a.Error("error msg")
	a.With(logField("a", "b")).Info("with msg")
	if !strings.Contains(buf.String(), "info msg") || !strings.Contains(buf.String(), "warn msg") {
		t.Fatalf("expected log output: %s", buf.String())
	}
}

func logField(k, v string) (f struct {
	Key   string
	Value any
}) {
	// Stub builder for the test — chain-commons logger.Field is a public
	// struct but we don't import its package here; use the local
	// composite shape.
	return struct {
		Key   string
		Value any
	}{Key: k, Value: v}
}
