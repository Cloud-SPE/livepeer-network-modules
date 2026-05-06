package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func findingsFor(t *testing.T, src string) []Finding {
	t.Helper()
	out, err := CheckSource("test.go", strings.NewReader(src))
	if err != nil {
		t.Fatalf("CheckSource: %v", err)
	}
	return out
}

func TestFlagsPasswordKey(t *testing.T) {
	src := `package p
import "log/slog"
func f(l *slog.Logger) { l.Info("login", "password", "hunter2") }`
	out := findingsFor(t, src)
	if len(out) != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", len(out), out)
	}
	if !strings.Contains(out[0].Msg, `"password"`) {
		t.Errorf("message should name the key: %q", out[0].Msg)
	}
	if out[0].RuleID != "nosecrets" {
		t.Errorf("rule id = %q, want nosecrets", out[0].RuleID)
	}
}

func TestFlagsVariousDenyListKeys(t *testing.T) {
	src := `package p
import "log/slog"
func f(l *slog.Logger) {
	l.Info("a", "passphrase", "x")
	l.Info("b", "private_key", "x")
	l.Info("c", "privateKey", "x")
	l.Info("d", "secret", "x")
	l.Info("e", "api_key", "x")
	l.Info("f", "apiKey", "x")
	l.Info("g", "keystore", "x")
	l.Info("h", "mnemonic", "x")
	l.Info("i", "auth_token", "x")
}`
	out := findingsFor(t, src)
	if len(out) != 9 {
		t.Fatalf("want 9 findings, got %d", len(out))
	}
}

func TestDoesNotFlagBenignKeys(t *testing.T) {
	src := `package p
import "log/slog"
func f(l *slog.Logger) {
	l.Info("event", "sender", "0xabc", "tx_hash", "0xdef", "nonce", 42)
}`
	out := findingsFor(t, src)
	if len(out) != 0 {
		t.Errorf("want 0 findings on benign keys, got: %+v", out)
	}
}

func TestCaseInsensitive(t *testing.T) {
	src := `package p
import "log/slog"
func f(l *slog.Logger) { l.Info("x", "USER_PASSWORD", "hunter2") }`
	out := findingsFor(t, src)
	if len(out) != 1 {
		t.Errorf("USER_PASSWORD should match (case-insensitive); got %d findings", len(out))
	}
}

func TestSubstringMatch(t *testing.T) {
	src := `package p
import "log/slog"
func f(l *slog.Logger) { l.Info("x", "old_password_hash", "...") }`
	out := findingsFor(t, src)
	if len(out) != 1 {
		t.Errorf("substring match should fire; got %d findings", len(out))
	}
}

func TestSuppressedBySameLineComment(t *testing.T) {
	src := `package p
import "log/slog"
func f(l *slog.Logger) { l.Info("x", "password", "h") //nolint:nosecrets // test
}`
	out := findingsFor(t, src)
	if len(out) != 0 {
		t.Errorf("same-line nolint should suppress; got: %+v", out)
	}
}

func TestSuppressedByLineAboveComment(t *testing.T) {
	src := `package p
import "log/slog"
func f(l *slog.Logger) {
	//nolint:nosecrets // explicitly fine
	l.Info("x", "password", "h")
}`
	out := findingsFor(t, src)
	if len(out) != 0 {
		t.Errorf("line-above nolint should suppress; got: %+v", out)
	}
}

func TestPackageLevelSlogCall(t *testing.T) {
	src := `package p
import "log/slog"
func f() { slog.Info("x", "api_key", "k") }`
	out := findingsFor(t, src)
	if len(out) != 1 {
		t.Errorf("package-level slog.Info should be flagged; got %d", len(out))
	}
}

func TestSlogLogWithContextAndLevel(t *testing.T) {
	src := `package p
import (
	"context"
	"log/slog"
)
func f() { slog.Log(context.Background(), slog.LevelInfo, "msg", "password", "h") }`
	out := findingsFor(t, src)
	if len(out) != 1 {
		t.Errorf("slog.Log should parse with argStart=3; got %d", len(out))
	}
}

func TestNonLiteralKeysIgnored(t *testing.T) {
	src := `package p
import (
	"fmt"
	"log/slog"
)
func f(l *slog.Logger) {
	k := fmt.Sprintf("%s_password", "user")
	l.Info("x", k, "h")
}`
	out := findingsFor(t, src)
	if len(out) != 0 {
		t.Errorf("non-literal keys must not be flagged in v1; got: %+v", out)
	}
}

func TestFindingString(t *testing.T) {
	f := Finding{
		Path: "a.go", Line: 5, Column: 2,
		RuleID: "nosecrets",
		Msg:    "x has key \"password\"",
	}
	s := f.String()
	for _, want := range []string{"a.go:5:2:", "nosecrets:", "Remediation:", "0017-warm-key-handling-design"} {
		if !strings.Contains(s, want) {
			t.Errorf("finding string missing %q: %q", want, s)
		}
	}
}

func TestCheckDirSkipsTestdataAndVendor(t *testing.T) {
	// testdata/ and vendor/ are excluded by the caller-provided skip func.
	findings, err := CheckDir("./testdata", func(path string) bool { return false })
	if err != nil {
		t.Fatalf("CheckDir: %v", err)
	}
	wantPaths := map[string]bool{"testdata/bad.go": true}
	for _, f := range findings {
		if !wantPaths[f.Path] {
			t.Errorf("unexpected finding: %+v", f)
		}
	}
}

func TestDefaultSkipExcludesConventional(t *testing.T) {
	cases := map[string]bool{
		"testdata":                          true,
		"some/path/testdata":                true,
		"vendor":                            true,
		"a/vendor":                          true,
		"proto/gen":                         true,
		"lint/no-secrets-in-logs":           true,
		"lint/no-secrets-in-logs/testdata":  true,
		"cmd/livepeer-payment-daemon":       false,
		"internal/service/sender":           false,
	}
	for path, wantSkip := range cases {
		if got := DefaultSkip(path); got != wantSkip {
			t.Errorf("DefaultSkip(%q) = %v, want %v", path, got, wantSkip)
		}
	}
}

func TestRunCleanExitsZero(t *testing.T) {
	tmp := t.TempDir()
	path := tmp + "/a.go"
	if err := writeString(path, `package p
import "log/slog"
func f(l *slog.Logger) { l.Info("event", "sender", "0xabc") }
`); err != nil {
		t.Fatalf("write: %v", err)
	}
	var buf bytes.Buffer
	if code := Run(tmp, &buf); code != 0 {
		t.Errorf("want exit 0 on clean tree, got %d; stderr=%q", code, buf.String())
	}
}

func TestRunFindingsExitOne(t *testing.T) {
	tmp := t.TempDir()
	path := tmp + "/a.go"
	if err := writeString(path, `package p
import "log/slog"
func f(l *slog.Logger) { l.Info("login", "password", "h") }
`); err != nil {
		t.Fatalf("write: %v", err)
	}
	var buf bytes.Buffer
	if code := Run(tmp, &buf); code != 1 {
		t.Errorf("want exit 1 on findings, got %d", code)
	}
	if !strings.Contains(buf.String(), "password") {
		t.Errorf("stderr should list the finding; got %q", buf.String())
	}
}

func TestRunIOError(t *testing.T) {
	var buf bytes.Buffer
	if code := Run("/does/not/exist", &buf); code != 2 {
		t.Errorf("want exit 2 on bad path, got %d", code)
	}
}

func TestCheckFileParseError(t *testing.T) {
	tmp := t.TempDir()
	path := tmp + "/bad.go"
	if err := writeString(path, "not valid go source"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := CheckFile(path); err == nil {
		t.Error("want parse error on malformed source")
	}
}

// TestRunPaymentDaemonTreeIsClean is a guard rail — when invoked from
// within the lint package, walks ../../ (the payment-daemon root) and
// asserts the production tree has zero findings. If a careless commit
// adds a `slog.Info("login", "password", …)` call it shows up here.
func TestRunPaymentDaemonTreeIsClean(t *testing.T) {
	var buf bytes.Buffer
	code := Run("../..", &buf)
	if code != 0 {
		t.Errorf("payment-daemon tree has secrets-in-logs findings (exit %d):\n%s", code, buf.String())
	}
}

// writeString is a tiny os.WriteFile shim so test helpers read cleanly.
func writeString(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
