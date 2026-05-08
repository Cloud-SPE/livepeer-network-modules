// Package main implements layer-check: a lint that verifies the
// layer-rule from AGENTS.md mechanically.
//
// Rules:
//   - internal/service/* may not import github.com/ethereum/* or go.etcd.io/bbolt
//   - internal/repo/* may not import go.etcd.io/bbolt directly (must go through
//     chain-commons.providers.store)
//   - internal/types/* may not import any internal/* sibling
//   - github.com/prometheus/client_golang may only be imported under
//     internal/runtime/metrics/ or cmd/
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Finding describes one violation.
type Finding struct {
	Path   string
	Line   int
	RuleID string
	Msg    string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", f.Path, f.Line, f.RuleID, f.Msg)
}

// Run walks `root` and returns exit code: 0 clean, 1 findings, 2 IO error.
func Run(root string, stderr io.Writer) int {
	findings, err := CheckRepo(root)
	if err != nil {
		fmt.Fprintf(stderr, "layer-check: %v\n", err)
		return 2
	}
	for _, f := range findings {
		fmt.Fprintln(stderr, f)
	}
	if len(findings) > 0 {
		fmt.Fprintln(stderr, "\nlayer-check: violations of the per-module layer rule.")
		fmt.Fprintln(stderr, "Remediation: route the disallowed dependency through internal/providers/")
		fmt.Fprintln(stderr, "(per-daemon) or chain-commons.providers/* (cross-module). See AGENTS.md.")
		return 1
	}
	return 0
}

// CheckRepo walks the repo and returns all findings.
func CheckRepo(root string) ([]Finding, error) {
	var out []Finding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if strings.HasPrefix(base, ".") || base == "vendor" || base == "bin" || base == "node_modules" || base == "testdata" || base == "gen" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip test files; lints don't enforce on tests.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		findings, err := CheckFile(root, path)
		if err != nil {
			return err
		}
		out = append(out, findings...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// CheckFile inspects one .go file's import block for layer violations.
func CheckFile(root, path string) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	rel, _ := filepath.Rel(root, path)

	var findings []Finding
	for _, imp := range file.Imports {
		ip := strings.Trim(imp.Path.Value, `"`)
		pos := fset.Position(imp.Pos())
		for _, rule := range rules {
			if rule.Forbid(rel, ip) {
				findings = append(findings, Finding{
					Path:   rel,
					Line:   pos.Line,
					RuleID: rule.ID,
					Msg:    rule.Msg(rel, ip),
				})
			}
		}
	}
	_ = ast.Inspect // keep ast import alive for future analysers
	return findings, nil
}

// Rule is one layer-check rule.
type Rule struct {
	ID     string
	Forbid func(file, importPath string) bool
	Msg    func(file, importPath string) string
}

var rules = []Rule{
	{
		ID: "service-no-eth",
		Forbid: func(file, ip string) bool {
			if !underInternal(file, "service") {
				return false
			}
			return strings.HasPrefix(ip, "github.com/ethereum/")
		},
		Msg: func(file, ip string) string {
			return fmt.Sprintf("%s imports %s; service code must use internal/providers/* (which wraps go-ethereum)", file, ip)
		},
	},
	{
		ID: "service-no-bbolt",
		Forbid: func(file, ip string) bool {
			if !underInternal(file, "service") {
				return false
			}
			return ip == "go.etcd.io/bbolt"
		},
		Msg: func(file, ip string) string {
			return fmt.Sprintf("%s imports %s; service code must use chain-commons.providers.store", file, ip)
		},
	},
	{
		ID: "repo-no-bbolt",
		Forbid: func(file, ip string) bool {
			if !underInternal(file, "repo") {
				return false
			}
			return ip == "go.etcd.io/bbolt"
		},
		Msg: func(file, ip string) string {
			return fmt.Sprintf("%s imports %s; repo code must use chain-commons.providers.store", file, ip)
		},
	},
	{
		ID: "types-no-internal",
		Forbid: func(file, ip string) bool {
			if !underInternal(file, "types") {
				return false
			}
			// types/ may not import any other internal/* sibling.
			return strings.Contains(ip, "/protocol-daemon/internal/") &&
				!strings.Contains(ip, "/internal/types")
		},
		Msg: func(file, ip string) string {
			return fmt.Sprintf("%s imports %s; types/ must not depend on any other internal package", file, ip)
		},
	},
	{
		ID: "prometheus-only-in-runtime-metrics-or-cmd",
		Forbid: func(file, ip string) bool {
			if !strings.HasPrefix(ip, "github.com/prometheus/client_golang") {
				return false
			}
			return !underInternal(file, "runtime/metrics") && !strings.HasPrefix(file, "cmd/")
		},
		Msg: func(file, ip string) string {
			return fmt.Sprintf("%s imports %s; only internal/runtime/metrics or cmd/ may use prometheus/client_golang", file, ip)
		},
	},
}

// underInternal reports whether `file` is under internal/<sub>/...
func underInternal(file, sub string) bool {
	return strings.HasPrefix(filepath.ToSlash(file), "internal/"+sub+"/") ||
		filepath.ToSlash(file) == "internal/"+sub
}
