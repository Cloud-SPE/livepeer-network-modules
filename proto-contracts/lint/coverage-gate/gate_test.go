package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageCoveragePercent(t *testing.T) {
	cases := []struct {
		name string
		pc   PackageCoverage
		want float64
	}{
		{"empty reports 100", PackageCoverage{Statements: 0, Covered: 0}, 100.0},
		{"half covered", PackageCoverage{Statements: 10, Covered: 5}, 50.0},
		{"all covered", PackageCoverage{Statements: 8, Covered: 8}, 100.0},
		{"none covered", PackageCoverage{Statements: 4, Covered: 0}, 0.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.pc.Percent(); got != c.want {
				t.Fatalf("got %.2f, want %.2f", got, c.want)
			}
		})
	}
}

func TestPackageOf(t *testing.T) {
	cases := map[string]string{
		"github.com/x/y/a/file.go":        "github.com/x/y/a",
		"github.com/x/y/deep/pkg/file.go": "github.com/x/y/deep/pkg",
		"toplevel.go":                     "toplevel.go",
	}
	for in, want := range cases {
		if got := PackageOf(in); got != want {
			t.Errorf("PackageOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseCoverageValid(t *testing.T) {
	input := `mode: atomic
github.com/x/y/a/file.go:1.1,2.2 3 1
github.com/x/y/a/file.go:2.2,3.3 2 0
github.com/x/y/b/other.go:5.5,6.6 4 7
`
	covs, err := ParseCoverage(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseCoverage: %v", err)
	}
	a, ok := covs["github.com/x/y/a"]
	if !ok {
		t.Fatalf("missing package a")
	}
	if a.Statements != 5 || a.Covered != 3 {
		t.Errorf("a: got statements=%d covered=%d, want 5/3", a.Statements, a.Covered)
	}
	b := covs["github.com/x/y/b"]
	if b.Statements != 4 || b.Covered != 4 {
		t.Errorf("b: got statements=%d covered=%d, want 4/4", b.Statements, b.Covered)
	}
}

func TestParseCoverageBadHeader(t *testing.T) {
	_, err := ParseCoverage(strings.NewReader("not a header\n"))
	if err == nil {
		t.Fatal("expected error for missing mode: header")
	}
}

func TestParseCoverageSkipsMalformed(t *testing.T) {
	input := `mode: set
garbage line
github.com/x/y/a/file.go:1.1,2.2 3 1
github.com/x/y/a/file.go:1.1,2.2 notanumber 1
github.com/x/y/a/file.go:1.1,2.2 2 notanumber
`
	covs, err := ParseCoverage(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseCoverage: %v", err)
	}
	a := covs["github.com/x/y/a"]
	if a.Statements != 3 || a.Covered != 3 {
		t.Fatalf("got %+v, want 3/3 for pkg a", a)
	}
}

func TestParseExemptions(t *testing.T) {
	input := `# comment
# another comment
github.com/x/y/cmd/foo  # main entry — tracked in #42
github.com/x/y/bar

# trailing comment
   github.com/x/y/baz   #   reason
`
	exempt, err := ParseExemptions(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseExemptions: %v", err)
	}
	want := []string{"github.com/x/y/cmd/foo", "github.com/x/y/bar", "github.com/x/y/baz"}
	for _, w := range want {
		if !exempt[w] {
			t.Errorf("missing exemption: %s (have %v)", w, exempt)
		}
	}
	if len(exempt) != 3 {
		t.Errorf("want 3 exemptions, got %d: %v", len(exempt), exempt)
	}
}

func TestEvaluateAllOK(t *testing.T) {
	covs := map[string]PackageCoverage{
		"pkg/a": {Pkg: "pkg/a", Statements: 10, Covered: 10},
	}
	r := Evaluate(covs, nil, []string{"pkg/a"}, nil, 75.0)
	if r.HasFailures() {
		t.Errorf("HasFailures = true, want false: %+v", r)
	}
	if len(r.OK) != 1 {
		t.Errorf("want 1 OK, got %d", len(r.OK))
	}
}

func TestEvaluateBelowThreshold(t *testing.T) {
	covs := map[string]PackageCoverage{
		"pkg/a": {Pkg: "pkg/a", Statements: 10, Covered: 5},
	}
	r := Evaluate(covs, nil, []string{"pkg/a"}, nil, 75.0)
	if !r.HasFailures() {
		t.Error("HasFailures = false, want true")
	}
	if len(r.Failing) != 1 {
		t.Fatalf("want 1 failing, got %d", len(r.Failing))
	}
}

func TestEvaluateExemption(t *testing.T) {
	covs := map[string]PackageCoverage{
		"pkg/a": {Pkg: "pkg/a", Statements: 10, Covered: 2},
	}
	r := Evaluate(covs, map[string]bool{"pkg/a": true}, []string{"pkg/a"}, nil, 75.0)
	if r.HasFailures() {
		t.Error("HasFailures = true, want false (exempt)")
	}
	if len(r.Exempt) != 1 {
		t.Fatalf("want 1 exempt, got %d", len(r.Exempt))
	}
}

func TestEvaluateMissingCoverage(t *testing.T) {
	// pkg/a has source but no coverage entry and no test files → Missing.
	r := Evaluate(map[string]PackageCoverage{}, nil, []string{"pkg/a"}, nil, 75.0)
	if !r.HasFailures() {
		t.Error("HasFailures = false, want true for missing coverage")
	}
	if len(r.Missing) != 1 || r.Missing[0] != "pkg/a" {
		t.Errorf("want missing=[pkg/a], got %v", r.Missing)
	}
}

func TestEvaluateInterfaceOnlyPackageWithTests(t *testing.T) {
	// pkg/a has source files and test files but no coverable statements
	// (e.g., an interface-only package). Go reports "[no statements]"
	// and writes nothing to coverage.out; the gate must treat this as OK.
	r := Evaluate(map[string]PackageCoverage{}, nil, []string{"pkg/a"}, map[string]bool{"pkg/a": true}, 75.0)
	if r.HasFailures() {
		t.Errorf("HasFailures = true, want false: %+v", r)
	}
	if len(r.OK) != 1 || r.OK[0].Pkg != "pkg/a" {
		t.Errorf("want OK=[pkg/a], got %+v", r.OK)
	}
}

func TestEvaluateExtraEntry(t *testing.T) {
	// pkg/gen is not in sourcePkgs but appears in covs (e.g., generated code
	// picked up by -coverpkg). It should be classified like any other package.
	covs := map[string]PackageCoverage{
		"pkg/a":   {Pkg: "pkg/a", Statements: 10, Covered: 10},
		"pkg/gen": {Pkg: "pkg/gen", Statements: 100, Covered: 5}, // 5% coverage
	}
	r := Evaluate(covs, map[string]bool{"pkg/gen": true}, []string{"pkg/a"}, nil, 75.0)
	if r.HasFailures() {
		t.Errorf("HasFailures = true, want false (extra is exempt): %+v", r)
	}
	if len(r.Exempt) != 1 || r.Exempt[0].Pkg != "pkg/gen" {
		t.Errorf("want extra pkg/gen in exempt, got %+v", r.Exempt)
	}

	// Same extra, but NOT exempt → classified as Failing.
	r2 := Evaluate(covs, nil, []string{"pkg/a"}, nil, 75.0)
	if len(r2.Failing) != 1 || r2.Failing[0].Pkg != "pkg/gen" {
		t.Errorf("want extra in failing, got %+v", r2.Failing)
	}
}

func TestEvaluateSorting(t *testing.T) {
	covs := map[string]PackageCoverage{
		"pkg/z": {Pkg: "pkg/z", Statements: 10, Covered: 10},
		"pkg/a": {Pkg: "pkg/a", Statements: 10, Covered: 10},
		"pkg/m": {Pkg: "pkg/m", Statements: 10, Covered: 10},
	}
	r := Evaluate(covs, nil, []string{"pkg/z", "pkg/a", "pkg/m"}, nil, 75.0)
	if len(r.OK) != 3 {
		t.Fatalf("want 3 OK, got %d", len(r.OK))
	}
	if r.OK[0].Pkg != "pkg/a" || r.OK[1].Pkg != "pkg/m" || r.OK[2].Pkg != "pkg/z" {
		t.Errorf("OK not sorted: %v", r.OK)
	}
}

func TestFormatReportFailing(t *testing.T) {
	r := Evaluate(map[string]PackageCoverage{
		"pkg/a": {Pkg: "pkg/a", Statements: 10, Covered: 8},
		"pkg/b": {Pkg: "pkg/b", Statements: 10, Covered: 3},
	}, nil, []string{"pkg/a", "pkg/b"}, nil, 75.0)
	out := FormatReport(r, 75.0)
	if !strings.Contains(out, "OK      pkg/a") {
		t.Errorf("missing OK line for pkg/a in:\n%s", out)
	}
	if !strings.Contains(out, "FAIL    pkg/b") {
		t.Errorf("missing FAIL line for pkg/b in:\n%s", out)
	}
	if !strings.Contains(out, "failing") {
		t.Errorf("missing failure footer in:\n%s", out)
	}
}

func TestFormatReportMissing(t *testing.T) {
	r := Evaluate(nil, nil, []string{"pkg/a"}, nil, 75.0)
	out := FormatReport(r, 75.0)
	if !strings.Contains(out, "MISSING pkg/a") {
		t.Errorf("missing MISSING line in:\n%s", out)
	}
}

func TestFormatReportAllPass(t *testing.T) {
	r := Evaluate(map[string]PackageCoverage{
		"pkg/a": {Pkg: "pkg/a", Statements: 10, Covered: 8},
	}, nil, []string{"pkg/a"}, nil, 75.0)
	out := FormatReport(r, 75.0)
	if !strings.Contains(out, "all packages meet the threshold") {
		t.Errorf("missing success footer in:\n%s", out)
	}
}

func TestFormatReportExemptWithAndWithoutStatements(t *testing.T) {
	// Exempt sourcePkg entry has no statements (synthetic placeholder).
	// Exempt extra coverage entry has statements.
	covs := map[string]PackageCoverage{
		"pkg/gen": {Pkg: "pkg/gen", Statements: 100, Covered: 5},
	}
	r := Evaluate(covs, map[string]bool{"pkg/a": true, "pkg/gen": true}, []string{"pkg/a"}, nil, 75.0)
	out := FormatReport(r, 75.0)
	if !strings.Contains(out, "EXEMPT  pkg/a") {
		t.Errorf("missing plain EXEMPT for pkg/a: %s", out)
	}
	if !strings.Contains(out, "EXEMPT  pkg/gen (") {
		t.Errorf("missing EXEMPT with percent for pkg/gen: %s", out)
	}
}

func TestRunOK(t *testing.T) {
	cov := `mode: atomic
x/a/f.go:1.1,2.2 10 1
x/a/f.go:2.2,3.3 5 1
`
	var stdout, stderr bytes.Buffer
	code := Run(strings.NewReader(cov), nil, []string{"x/a"}, nil, 75.0, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "OK      x/a") {
		t.Errorf("missing OK line: %q", stdout.String())
	}
}

func TestRunFail(t *testing.T) {
	cov := `mode: atomic
x/a/f.go:1.1,2.2 10 1
x/a/f.go:2.2,3.3 10 0
`
	var stdout, stderr bytes.Buffer
	code := Run(strings.NewReader(cov), nil, []string{"x/a"}, nil, 75.0, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "Remediation") {
		t.Errorf("missing remediation: %q", stderr.String())
	}
}

func TestRunParseError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(strings.NewReader("bogus\n"), nil, nil, nil, 75.0, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("want exit 2, got %d", code)
	}
}

func TestRunMissing(t *testing.T) {
	cov := "mode: atomic\n"
	var stdout, stderr bytes.Buffer
	code := Run(strings.NewReader(cov), nil, []string{"x/a"}, nil, 75.0, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("want exit 1 for missing, got %d", code)
	}
	if !strings.Contains(stdout.String(), "MISSING x/a") {
		t.Errorf("missing MISSING line: %q", stdout.String())
	}
}

func TestRunWithExempt(t *testing.T) {
	cov := `mode: atomic
x/a/f.go:1.1,2.2 10 0
`
	ex := "x/a\n"
	var stdout, stderr bytes.Buffer
	code := Run(strings.NewReader(cov), strings.NewReader(ex), []string{"x/a"}, nil, 75.0, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit 0 via exemption, got %d", code)
	}
}

func TestRunExemptionsParseError(t *testing.T) {
	cov := "mode: atomic\n"
	var stdout, stderr bytes.Buffer
	code := Run(strings.NewReader(cov), errReader{}, nil, nil, 75.0, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("want exit 2, got %d", code)
	}
}

type errReader struct{}

func (errReader) Read(_ []byte) (int, error) { return 0, readError("forced") }

type readError string

func (e readError) Error() string { return string(e) }

func TestDiscoverSourcePackages(t *testing.T) {
	root := t.TempDir()
	// Write a go.mod with a module path.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/x\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	// A package with a source file and a test file.
	mustMkdir(t, root, "pkg/a")
	mustWrite(t, root, "pkg/a/a.go", "package a\n")
	mustWrite(t, root, "pkg/a/a_test.go", "package a\n")
	// A package with only a test file (no source).
	mustMkdir(t, root, "pkg/b")
	mustWrite(t, root, "pkg/b/b_test.go", "package b\n")
	// A directory to skip (.git).
	mustMkdir(t, root, ".git")
	mustWrite(t, root, ".git/config", "[core]\n")
	// A sibling module (must skip).
	mustMkdir(t, root, "pkg/c")
	mustWrite(t, root, "pkg/c/go.mod", "module example.com/c\n\ngo 1.25\n")
	mustWrite(t, root, "pkg/c/c.go", "package c\n")
	// Root-level source file, no tests.
	mustWrite(t, root, "main.go", "package main\n")
	// Interface-only package (source file but no test file).
	mustMkdir(t, root, "pkg/iface")
	mustWrite(t, root, "pkg/iface/iface.go", "package iface\n")

	pkgs, hasTests, err := DiscoverSourcePackages(root)
	if err != nil {
		t.Fatalf("DiscoverSourcePackages: %v", err)
	}
	want := map[string]bool{
		"example.com/x":           true,
		"example.com/x/pkg/a":     true,
		"example.com/x/pkg/iface": true,
	}
	got := map[string]bool{}
	for _, p := range pkgs {
		got[p] = true
	}
	for w := range want {
		if !got[w] {
			t.Errorf("missing expected package %q (have %v)", w, pkgs)
		}
	}
	if got["example.com/x/pkg/b"] {
		t.Errorf("pkg/b has only a test file; should not be in source packages")
	}
	if got["example.com/x/pkg/c"] {
		t.Errorf("pkg/c is a sibling module; should be skipped")
	}

	// hasTests map
	if !hasTests["example.com/x/pkg/a"] {
		t.Error("pkg/a has a test file; should be in hasTests")
	}
	if hasTests["example.com/x/pkg/iface"] {
		t.Error("pkg/iface has no test file; should not be in hasTests")
	}
	if hasTests["example.com/x"] {
		t.Error("root has no test file; should not be in hasTests")
	}
}

func TestDiscoverSourcePackagesMissingGoMod(t *testing.T) {
	root := t.TempDir()
	_, _, err := DiscoverSourcePackages(root)
	if err == nil {
		t.Fatal("expected error when go.mod missing")
	}
}

func TestDiscoverSourcePackagesInvalidGoMod(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("// no module directive\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, _, err := DiscoverSourcePackages(root)
	if err == nil {
		t.Fatal("expected error for missing module directive")
	}
}

func mustMkdir(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
}

func mustWrite(t *testing.T, root, rel, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
		t.Fatalf("mkdir parent of %s: %v", rel, err)
	}
	if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
