package logger

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestSlog_EmitsAtAllLevels(t *testing.T) {
	var buf bytes.Buffer
	l := Slog(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	l.Debug("debug-msg", String("k", "v"))
	l.Info("info-msg", Int("count", 42))
	l.Warn("warn-msg", Err(nil))
	l.Error("error-msg", Uint64("nonce", 7))

	out := buf.String()
	for _, want := range []string{"debug-msg", "info-msg", "warn-msg", "error-msg", "k=v", "count=42", "nonce=7"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestSlogJSON(t *testing.T) {
	var buf bytes.Buffer
	l := SlogJSON(&buf, nil)
	l.Info("hello", String("world", "yes"))
	if !strings.Contains(buf.String(), `"world":"yes"`) {
		t.Errorf("JSON output missing field: %s", buf.String())
	}
}

func TestWith_AddsFields(t *testing.T) {
	var buf bytes.Buffer
	l := Slog(&buf, nil).With(String("module", "txintent"))
	l.Info("evt")
	if !strings.Contains(buf.String(), "module=txintent") {
		t.Errorf("With did not propagate fields: %s", buf.String())
	}
}

func TestFields(t *testing.T) {
	if (String("k", "v")).Key != "k" {
		t.Errorf("String key wrong")
	}
	if (Int("k", 1)).Value.(int) != 1 {
		t.Errorf("Int value wrong")
	}
	if (Uint64("k", 1)).Value.(uint64) != 1 {
		t.Errorf("Uint64 value wrong")
	}
	if (Err(nil)).Key != "err" {
		t.Errorf("Err key wrong")
	}
	if (Any("k", "v")).Key != "k" {
		t.Errorf("Any key wrong")
	}
}
