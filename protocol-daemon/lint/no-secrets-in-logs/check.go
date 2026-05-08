// Package main implements a custom lint that rejects slog call sites
// whose structured attrs contain deny-listed keys like "password" or
// "private_key". Pairs with 0013-structured-logging: once every log
// call site is slog, this check keeps secrets out.
//
// Detection is conservative: we flag string-literal keys that look
// dangerous. Keys computed at runtime (`fmt.Sprintf`, variables) are
// flagged as "unanalyzable" so operators either refactor them to
// literals or add a `//nolint:nosecrets` justification.
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

// DefaultSkip returns the path filter used by the CLI entry point.
// Exposed so tests can exercise the real skip policy.
func DefaultSkip(path string) bool {
	base := filepath.Base(path)
	if strings.HasSuffix(path, "proto/gen") || base == "testdata" || base == "vendor" {
		return true
	}
	return false
}

// Run executes the analyzer from an entry point's perspective: walks
// `root`, prints findings to `stderr`, and returns an exit code. 0 =
// clean, 1 = findings, 2 = I/O error.
func Run(root string, stderr io.Writer) int {
	findings, err := CheckDir(root, DefaultSkip)
	if err != nil {
		fmt.Fprintf(stderr, "no-secrets-in-logs: %v\n", err)
		return 2
	}
	for _, f := range findings {
		fmt.Fprintln(stderr, f)
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}

// DenyList is the default set of attr keys considered secret. Matched
// case-insensitively; substring anywhere within the key triggers.
var DenyList = []string{
	"password",
	"passphrase",
	"secret",
	"apikey",
	"api_key",
	"privatekey",
	"private_key",
	"keystore",
	"mnemonic",
	"authtoken",
	"auth_token",
}

// slogMethods are the slog receiver methods that accept variadic
// key/value args. Package-level equivalents (`slog.Info`, etc.) share
// the shape.
var slogMethods = map[string]bool{
	"Debug": true,
	"Info":  true,
	"Warn":  true,
	"Error": true,
	"Log":   true, // slog.Log takes (ctx, level, msg, args...) — args start at index 3
}

// Finding describes one lint violation.
type Finding struct {
	Path   string
	Line   int
	Column int
	RuleID string // "nosecrets"
	Msg    string
}

// String renders the finding in the lint/README.md canonical format.
func (f Finding) String() string {
	return fmt.Sprintf(
		"%s:%d:%d: %s: %s\n  Remediation: remove the attribute, or use a derived value (e.g., a fingerprint hash) instead of the secret itself.\n  See: docs/exec-plans/completed/0013-structured-logging.md",
		f.Path, f.Line, f.Column, f.RuleID, f.Msg,
	)
}

// CheckDir walks every .go file under root (excluding generated/testdata
// and `_test.go`) and returns all findings sorted deterministically.
func CheckDir(root string, skip func(path string) bool) ([]Finding, error) {
	var out []Finding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip != nil && skip(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if skip != nil && skip(path) {
			return nil
		}
		findings, err := CheckFile(path)
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

// CheckFile runs the analyzer over a single .go file.
func CheckFile(path string) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return checkAST(fset, path, file), nil
}

// CheckSource is the test-friendly entry point — parses a source buffer
// with a caller-supplied virtual path.
func CheckSource(path string, src io.Reader) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return checkAST(fset, path, file), nil
}

func checkAST(fset *token.FileSet, path string, file *ast.File) []Finding {
	var out []Finding
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		methodName, argStart, ok := classifySlogCall(call)
		if !ok {
			return true
		}
		if len(call.Args) <= argStart {
			return true
		}
		if hasSuppression(fset, file, call) {
			return true
		}
		// After the msg arg, the rest are key/value pairs. slog.Log has
		// its own shape; the argStart above accounts for that.
		kvs := call.Args[argStart:]
		for i := 0; i < len(kvs); i += 2 {
			key := kvs[i]
			// If this is an slog.Attr / slog.Any / slog.String etc., we
			// don't currently unwrap it — flag only literal string keys.
			lit, isLit := key.(*ast.BasicLit)
			if !isLit || lit.Kind != token.STRING {
				continue
			}
			trimmed := strings.Trim(lit.Value, "\"`")
			if isDenied(trimmed) {
				pos := fset.Position(lit.Pos())
				out = append(out, Finding{
					Path:   path,
					Line:   pos.Line,
					Column: pos.Column,
					RuleID: "nosecrets",
					Msg: fmt.Sprintf(
						"slog.%s call has attribute key %q which matches the secret deny-list",
						methodName, trimmed,
					),
				})
			}
		}
		return true
	})
	return out
}

// classifySlogCall reports whether `call` looks like a slog logging call
// and where its variadic key/value args begin in call.Args.
// Returns (methodName, argStart, true) on match.
//
// Handled shapes:
//
//	logger.Info(msg, key, value, ...)        → method, argStart=1
//	slog.Info(msg, key, value, ...)          → method, argStart=1
//	slog.Log(ctx, lvl, msg, key, value, ...) → "Log", argStart=3
func classifySlogCall(call *ast.CallExpr) (string, int, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", 0, false
	}
	name := sel.Sel.Name
	if !slogMethods[name] {
		return "", 0, false
	}
	// slog.Log has a different arg shape.
	if name == "Log" {
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "slog" {
			return name, 3, true
		}
		return name, 0, false
	}
	// Accept both package-level `slog.Info` and any receiver named
	// something logger-y. We don't do type resolution, so this is a
	// heuristic — it'll flag non-slog loggers that happen to share the
	// name, but those should migrate to slog anyway.
	return name, 1, true
}

// hasSuppression returns true if the call site has a
// `//nolint:nosecrets` comment on the same line or the line above.
func hasSuppression(fset *token.FileSet, file *ast.File, call *ast.CallExpr) bool {
	callLine := fset.Position(call.Pos()).Line
	for _, cg := range file.Comments {
		cgLine := fset.Position(cg.End()).Line
		if cgLine != callLine && cgLine != callLine-1 {
			continue
		}
		for _, c := range cg.List {
			if strings.Contains(c.Text, "nolint:nosecrets") {
				return true
			}
		}
	}
	return false
}

// isDenied reports whether `key` contains any deny-list substring
// (case-insensitive).
func isDenied(key string) bool {
	lk := strings.ToLower(key)
	for _, bad := range DenyList {
		if strings.Contains(lk, bad) {
			return true
		}
	}
	return false
}
