package logger

import (
	"bytes"
	"strings"
	"testing"
)

func TestNew_TextFormat(t *testing.T) {
	var buf bytes.Buffer
	l := New(Config{Level: "debug", Format: "text", W: &buf})
	l.Info("hello", "k", "v")
	out := buf.String()
	if !strings.Contains(out, "hello") || !strings.Contains(out, "k=v") {
		t.Fatalf("text output missing fields: %s", out)
	}
}

func TestNew_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	l := New(Config{Level: "info", Format: "json", W: &buf})
	l.Warn("warning", "scope", "test")
	out := buf.String()
	if !strings.Contains(out, `"msg":"warning"`) {
		t.Fatalf("json output missing msg: %s", out)
	}
	if !strings.Contains(out, `"scope":"test"`) {
		t.Fatalf("json output missing kv: %s", out)
	}
}

func TestWith_AddsAttributes(t *testing.T) {
	var buf bytes.Buffer
	l := New(Config{Level: "debug", Format: "text", W: &buf})
	child := l.With("scope", "outer")
	child.Info("hello")
	out := buf.String()
	if !strings.Contains(out, "scope=outer") {
		t.Fatalf("With did not add scope: %s", out)
	}
}

func TestAttrs_HandlesOddKV(t *testing.T) {
	var buf bytes.Buffer
	l := New(Config{Level: "debug", Format: "text", W: &buf})
	// Odd-length: trailing "leftover" is dropped silently.
	l.Info("hi", "k1", "v1", "leftover")
	out := buf.String()
	if !strings.Contains(out, "k1=v1") {
		t.Fatalf("attrs broke on odd-length input: %s", out)
	}
}

func TestDiscard_DoesNotPanic(t *testing.T) {
	d := Discard()
	d.Debug("d")
	d.Info("i")
	d.Warn("w")
	d.Error("e")
}
