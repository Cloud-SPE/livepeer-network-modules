// Package main contains the coverage-gate CI tool. This file holds the
// testable pure-logic primitives; main.go wires them to the filesystem and
// os.Exit.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// PackageCoverage accumulates statement/covered counts for a single package.
type PackageCoverage struct {
	Pkg        string
	Statements int
	Covered    int
}

// Percent returns coverage in [0, 100]. An empty package reports 100.
func (p PackageCoverage) Percent() float64 {
	if p.Statements == 0 {
		return 100.0
	}
	return 100.0 * float64(p.Covered) / float64(p.Statements)
}

// Report captures the result of an evaluation.
type Report struct {
	OK      []PackageCoverage
	Exempt  []PackageCoverage
	Failing []PackageCoverage
	Missing []string // packages with source files but no coverage data
}

// HasFailures reports whether any non-exempt package was below threshold or
// missing coverage data.
func (r Report) HasFailures() bool { return len(r.Failing) > 0 || len(r.Missing) > 0 }

// ParseCoverage reads the text format written by `go test -coverprofile=` and
// returns per-package accumulated coverage.
func ParseCoverage(r io.Reader) (map[string]PackageCoverage, error) {
	pkgs := make(map[string]PackageCoverage)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			if !strings.HasPrefix(line, "mode:") {
				return nil, fmt.Errorf("unexpected coverage header: %q", line)
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		file := strings.SplitN(fields[0], ":", 2)[0]
		statements, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		pkg := PackageOf(file)
		pc := pkgs[pkg]
		pc.Pkg = pkg
		pc.Statements += statements
		if count > 0 {
			pc.Covered += statements
		}
		pkgs[pkg] = pc
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return pkgs, nil
}

// PackageOf returns the package import path a coverage source file belongs
// to, i.e. the file path without its final element.
func PackageOf(file string) string {
	idx := strings.LastIndex(file, "/")
	if idx < 0 {
		return file
	}
	return file[:idx]
}

// ParseExemptions reads the exemption file format. Lines may contain inline
// `# reason` comments; comment-only lines and blank lines are ignored.
// Returns the set of exempt package import paths.
func ParseExemptions(r io.Reader) (map[string]bool, error) {
	m := map[string]bool{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if hash := strings.Index(line, "#"); hash >= 0 {
			line = strings.TrimSpace(line[:hash])
		}
		if line == "" {
			continue
		}
		m[line] = true
	}
	return m, sc.Err()
}

// Evaluate classifies each package against the exemption set, threshold, and
// the list of known source-bearing packages.
//
// `hasTests` maps source-bearing package import paths to whether the
// package contains at least one `_test.go` file. A source package without
// coverage data is tolerated ONLY if it has test files — that covers
// interface-only packages (all type / const / var declarations, no function
// bodies), which Go reports as "[no statements]" and does not write to
// coverage.out at all.
//
// Source packages with no test files and no coverage are reported in
// Missing and fail the gate.
func Evaluate(covs map[string]PackageCoverage, exempt map[string]bool, sourcePkgs []string, hasTests map[string]bool, threshold float64) Report {
	var r Report

	// Sort sourcePkgs for determinism and check each.
	sortedSrc := append([]string(nil), sourcePkgs...)
	sort.Strings(sortedSrc)
	seen := map[string]bool{}
	for _, p := range sortedSrc {
		seen[p] = true
		if exempt[p] {
			r.Exempt = append(r.Exempt, PackageCoverage{Pkg: p})
			continue
		}
		c, ok := covs[p]
		if !ok {
			if hasTests[p] {
				// Package has tests but no coverable statements — Go
				// reports "[no statements]" and writes nothing. OK.
				r.OK = append(r.OK, PackageCoverage{Pkg: p})
				continue
			}
			r.Missing = append(r.Missing, p)
			continue
		}
		if c.Percent() < threshold {
			r.Failing = append(r.Failing, c)
		} else {
			r.OK = append(r.OK, c)
		}
	}

	// Also emit coverage entries that were not in sourcePkgs (e.g., generated
	// code captured via -coverpkg). These are informational and do not affect
	// pass/fail unless explicitly failing.
	extras := make([]string, 0)
	for k := range covs {
		if !seen[k] {
			extras = append(extras, k)
		}
	}
	sort.Strings(extras)
	for _, k := range extras {
		c := covs[k]
		if exempt[k] {
			r.Exempt = append(r.Exempt, c)
		} else if c.Percent() < threshold {
			r.Failing = append(r.Failing, c)
		} else {
			r.OK = append(r.OK, c)
		}
	}
	return r
}

// FormatReport renders a report for human consumption.
func FormatReport(r Report, threshold float64) string {
	var b strings.Builder
	for _, c := range r.OK {
		_, _ = fmt.Fprintf(&b, "  OK      %s (%.1f%%)\n", c.Pkg, c.Percent())
	}
	for _, c := range r.Exempt {
		if c.Statements > 0 {
			_, _ = fmt.Fprintf(&b, "  EXEMPT  %s (%.1f%%)\n", c.Pkg, c.Percent())
		} else {
			_, _ = fmt.Fprintf(&b, "  EXEMPT  %s\n", c.Pkg)
		}
	}
	for _, c := range r.Failing {
		_, _ = fmt.Fprintf(&b, "  FAIL    %s (%.1f%% < %.0f%%)\n", c.Pkg, c.Percent(), threshold)
	}
	for _, p := range r.Missing {
		_, _ = fmt.Fprintf(&b, "  MISSING %s (no tests — add at least one _test.go)\n", p)
	}
	if r.HasFailures() {
		total := len(r.Failing) + len(r.Missing)
		_, _ = fmt.Fprintf(&b, "\ncoverage-gate: %d package(s) failing (%.0f%% threshold)\n", total, threshold)
	} else {
		_, _ = b.WriteString("\ncoverage-gate: all packages meet the threshold.\n")
	}
	return b.String()
}

// Run is the full gate logic against in-memory readers, with an explicit
// list of source-bearing packages that must be tested. Returns the process
// exit code.
//
//	0 = all packages pass
//	1 = at least one non-exempt package below threshold OR missing coverage
//	2 = input parse error
func Run(cov io.Reader, exempt io.Reader, sourcePkgs []string, hasTests map[string]bool, threshold float64, stdout, stderr io.Writer) int {
	covs, err := ParseCoverage(cov)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage-gate: %v\n", err)
		return 2
	}
	em := map[string]bool{}
	if exempt != nil {
		em, err = ParseExemptions(exempt)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "coverage-gate: %v\n", err)
			return 2
		}
	}
	report := Evaluate(covs, em, sourcePkgs, hasTests, threshold)
	_, _ = fmt.Fprint(stdout, FormatReport(report, threshold))
	if report.HasFailures() {
		_, _ = fmt.Fprintln(stderr, "\nRemediation: add tests to the listed packages. If coverage is inherently")
		_, _ = fmt.Fprintln(stderr, "impractical, add the package to lint/coverage-gate/exemptions.txt with a")
		_, _ = fmt.Fprintln(stderr, "written reason and a tracking issue.")
		return 1
	}
	return 0
}

// DiscoverSourcePackages walks the given root directory and returns:
//   - the sorted set of import paths of Go packages that contain at least
//     one non-test .go source file
//   - a map from those import paths to true iff the package also contains
//     at least one `_test.go` file
//
// The module path is derived from go.mod in root. Sibling modules (any
// subdir containing its own go.mod) are skipped — they are tested
// independently.
//
// This deliberately does not call `go list`; the filesystem walk is
// enough and keeps the tool's dependencies minimal.
func DiscoverSourcePackages(root string) ([]string, map[string]bool, error) {
	modPath, err := readModulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, nil, err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	hasSource := map[string]bool{}
	hasTests := map[string]bool{}

	err = filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			// Skip hidden dirs and build / vendoring dirs.
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "bin" || name == "dist" || name == "node_modules" {
				return filepath.SkipDir
			}
			// Skip sibling modules — they are tested independently.
			if path != absRoot {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		name := info.Name()
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		dir := filepath.Dir(path)
		rel, err := filepath.Rel(absRoot, dir)
		if err != nil {
			return err
		}
		var importPath string
		if rel == "." {
			importPath = modPath
		} else {
			importPath = modPath + "/" + filepath.ToSlash(rel)
		}
		if strings.HasSuffix(name, "_test.go") {
			hasTests[importPath] = true
			return nil
		}
		hasSource[importPath] = true
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	pkgs := make([]string, 0, len(hasSource))
	for p := range hasSource {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	return pkgs, hasTests, nil
}

// readModulePath returns the module path declared in a go.mod file.
func readModulePath(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", fmt.Errorf("no module directive in %s", path)
}
