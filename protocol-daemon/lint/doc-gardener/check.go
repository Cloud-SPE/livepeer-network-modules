// Package main implements doc-gardener: a lint that validates frontmatter
// + internal cross-links for design-docs and exec-plans.
//
// Design-doc requirements (docs/design-docs/*.md):
//   - title, status, last-reviewed frontmatter present
//   - status ∈ {proposed, accepted, verified, deprecated}
//   - last-reviewed parses as YYYY-MM-DD
//
// Exec-plan requirements (docs/exec-plans/{active,completed}/*.md):
//   - id, slug, title, status, opened present
//   - status = "active" for files under active/, status = "completed" for
//     files under completed/
//   - completed plans have started + completed dates that parse
//
// Cross-link requirements (both):
//   - Every [text](path.md) and [text](path/file.md) link resolves to
//     an existing file on disk.
//   - Anchors (#section) are not checked in v1.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Finding describes a validation violation.
type Finding struct {
	Path   string
	Line   int
	RuleID string
	Msg    string
}

func (f Finding) String() string {
	return fmt.Sprintf(
		"%s:%d: %s: %s\n  Remediation: update the doc to satisfy the rule; see lint/README.md for doc-gardener's ruleset.",
		f.Path, f.Line, f.RuleID, f.Msg,
	)
}

var (
	// Frontmatter between leading `---` fences, captured in one group.
	frontmatterRE = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n`)

	// A single frontmatter entry: `key: value` on its own line (no list
	// / nested parse — our docs don't need it).
	kvLineRE = regexp.MustCompile(`^([a-z][a-z0-9_-]*):\s*(.*?)\s*$`)

	// Relative markdown links: [text](target). Absolute URLs
	// (http/https/mailto/ftp, or starting with #) are skipped.
	linkRE = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)

	validDocStatus  = map[string]bool{"proposed": true, "accepted": true, "verified": true, "deprecated": true}
	validPlanStatus = map[string]bool{"active": true, "completed": true}
)

// CheckRepo walks the conventional doc roots and returns all findings.
// `repoRoot` is the directory containing docs/.
func CheckRepo(repoRoot string) ([]Finding, error) {
	var out []Finding

	designDocsDir := filepath.Join(repoRoot, "docs", "design-docs")
	if entries, err := collectMarkdown(designDocsDir); err == nil {
		for _, path := range entries {
			// index.md is a listing, not a design-doc itself — no
			// frontmatter required. Still cross-link-checked.
			isIndex := filepath.Base(path) == "index.md"
			out = append(out, checkDesignDoc(path, isIndex)...)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	activePlansDir := filepath.Join(repoRoot, "docs", "exec-plans", "active")
	if entries, err := collectMarkdown(activePlansDir); err == nil {
		for _, path := range entries {
			out = append(out, checkPlan(path, "active")...)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	completedPlansDir := filepath.Join(repoRoot, "docs", "exec-plans", "completed")
	if entries, err := collectMarkdown(completedPlansDir); err == nil {
		for _, path := range entries {
			out = append(out, checkPlan(path, "completed")...)
		}
	} else if !os.IsNotExist(err) {
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

func collectMarkdown(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out, nil
}

// checkDesignDoc enforces design-doc rules on a single file.
func checkDesignDoc(path string, isIndex bool) []Finding {
	data, err := os.ReadFile(path) //nolint:gosec // G304: caller-controlled lint tool
	if err != nil {
		return []Finding{{Path: path, Line: 0, RuleID: "io", Msg: err.Error()}}
	}
	var findings []Finding
	if !isIndex {
		fm, fmLine, fmErr := parseFrontmatter(data)
		if fmErr != "" {
			findings = append(findings, Finding{Path: path, Line: 1, RuleID: "frontmatter", Msg: fmErr})
		} else {
			findings = append(findings, requireDesignDocFrontmatter(path, fm, fmLine)...)
		}
	}
	findings = append(findings, checkLinks(path, data)...)
	return findings
}

func requireDesignDocFrontmatter(path string, fm map[string]string, baseLine int) []Finding {
	var out []Finding
	for _, key := range []string{"title", "status", "last-reviewed"} {
		if _, ok := fm[key]; !ok {
			out = append(out, Finding{Path: path, Line: baseLine, RuleID: "frontmatter", Msg: "missing required key " + key})
		}
	}
	if status, ok := fm["status"]; ok && !validDocStatus[status] {
		out = append(out, Finding{
			Path: path, Line: baseLine, RuleID: "frontmatter",
			Msg: fmt.Sprintf("status=%q must be one of proposed|accepted|verified|deprecated", status),
		})
	}
	if reviewed, ok := fm["last-reviewed"]; ok {
		if _, err := time.Parse("2006-01-02", reviewed); err != nil {
			out = append(out, Finding{
				Path: path, Line: baseLine, RuleID: "frontmatter",
				Msg: fmt.Sprintf("last-reviewed=%q must be YYYY-MM-DD", reviewed),
			})
		}
	}
	return out
}

// checkPlan enforces exec-plan rules on a single file.
func checkPlan(path, expectedStatus string) []Finding {
	data, err := os.ReadFile(path) //nolint:gosec // G304: caller-controlled lint tool
	if err != nil {
		return []Finding{{Path: path, Line: 0, RuleID: "io", Msg: err.Error()}}
	}
	var findings []Finding
	fm, fmLine, fmErr := parseFrontmatter(data)
	if fmErr != "" {
		findings = append(findings, Finding{Path: path, Line: 1, RuleID: "frontmatter", Msg: fmErr})
	} else {
		findings = append(findings, requirePlanFrontmatter(path, fm, fmLine, expectedStatus)...)
	}
	findings = append(findings, checkLinks(path, data)...)
	return findings
}

func requirePlanFrontmatter(path string, fm map[string]string, baseLine int, expectedStatus string) []Finding {
	var out []Finding
	for _, key := range []string{"id", "slug", "title", "status", "opened"} {
		if _, ok := fm[key]; !ok {
			out = append(out, Finding{Path: path, Line: baseLine, RuleID: "frontmatter", Msg: "missing required key " + key})
		}
	}
	if status, ok := fm["status"]; ok {
		if !validPlanStatus[status] {
			out = append(out, Finding{
				Path: path, Line: baseLine, RuleID: "frontmatter",
				Msg: fmt.Sprintf("status=%q must be one of active|completed", status),
			})
		} else if status != expectedStatus {
			out = append(out, Finding{
				Path: path, Line: baseLine, RuleID: "frontmatter",
				Msg: fmt.Sprintf("status=%q does not match directory (plan lives in %s/)", status, expectedStatus),
			})
		}
	}
	for _, key := range []string{"opened"} {
		if v, ok := fm[key]; ok {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				out = append(out, Finding{
					Path: path, Line: baseLine, RuleID: "frontmatter",
					Msg: fmt.Sprintf("%s=%q must be YYYY-MM-DD", key, v),
				})
			}
		}
	}
	if expectedStatus == "completed" {
		// `started` is nice-to-have; `completed` (or the historical alias
		// `closed`) is required. Date format enforced on both.
		if v, ok := fm["started"]; ok {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				out = append(out, Finding{
					Path: path, Line: baseLine, RuleID: "frontmatter",
					Msg: fmt.Sprintf("started=%q must be YYYY-MM-DD", v),
				})
			}
		}
		completedVal, ok := fm["completed"]
		if !ok {
			completedVal, ok = fm["closed"]
		}
		if !ok {
			out = append(out, Finding{Path: path, Line: baseLine, RuleID: "frontmatter", Msg: "completed plan must have completed (or closed) date"})
		} else if _, err := time.Parse("2006-01-02", completedVal); err != nil {
			out = append(out, Finding{
				Path: path, Line: baseLine, RuleID: "frontmatter",
				Msg: fmt.Sprintf("completed/closed=%q must be YYYY-MM-DD", completedVal),
			})
		}
	}
	return out
}

// parseFrontmatter returns the key/value map, the line number of the
// first frontmatter content line (1-indexed), and an error message
// string (empty when parse succeeds).
func parseFrontmatter(data []byte) (map[string]string, int, string) {
	match := frontmatterRE.FindSubmatchIndex(data)
	if match == nil {
		return nil, 0, "missing `---` frontmatter at top of file"
	}
	body := string(data[match[2]:match[3]])
	fm := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		if m := kvLineRE.FindStringSubmatch(line); m != nil {
			fm[m[1]] = strings.Trim(m[2], `"'`)
		}
	}
	return fm, 2, ""
}

// checkLinks scans `data` for relative markdown links and verifies each
// target file exists. File paths are resolved against the directory of
// `path`. Links inside fenced code blocks (``` ... ```) and inline code
// spans (` ... `) are deliberately skipped — they're examples, not
// real links.
func checkLinks(path string, data []byte) []Finding {
	var out []Finding
	dir := filepath.Dir(path)
	stripped := stripCode(data)
	matches := linkRE.FindAllIndex(stripped, -1)
	for _, m := range matches {
		raw := string(stripped[m[0]:m[1]])
		sub := linkRE.FindStringSubmatch(raw)
		if len(sub) < 2 {
			continue
		}
		target := sub[1]
		if shouldSkipLink(target) {
			continue
		}
		// Strip any #anchor or ?query suffix.
		if idx := strings.IndexAny(target, "#?"); idx >= 0 {
			target = target[:idx]
		}
		if target == "" {
			continue
		}
		resolved := filepath.Clean(filepath.Join(dir, target))
		if _, err := os.Stat(resolved); err != nil {
			line := byteOffsetToLine(stripped, m[0])
			out = append(out, Finding{
				Path: path, Line: line, RuleID: "broken-link",
				Msg: fmt.Sprintf("link target %q does not exist at %s", sub[1], resolved),
			})
		}
	}
	return out
}

// stripCode replaces fenced code blocks and inline code spans with
// whitespace of the same length, so downstream link scanning doesn't
// match example link syntax inside code. Byte offsets + line numbers
// stay consistent because we preserve length.
func stripCode(data []byte) []byte {
	out := make([]byte, len(data))
	copy(out, data)

	// Fenced code blocks: ```...```. Use regexp for simplicity; greedy
	// match across newlines.
	fenced := regexp.MustCompile("(?s)```.*?```")
	for _, m := range fenced.FindAllIndex(out, -1) {
		blankSpan(out, m[0], m[1])
	}
	// Inline code spans: `...`. Single-line only.
	inline := regexp.MustCompile("`[^`\n]*`")
	for _, m := range inline.FindAllIndex(out, -1) {
		blankSpan(out, m[0], m[1])
	}
	return out
}

func blankSpan(out []byte, start, end int) {
	for i := start; i < end; i++ {
		if out[i] != '\n' {
			out[i] = ' '
		}
	}
}

func shouldSkipLink(target string) bool {
	if target == "" {
		return true
	}
	if strings.HasPrefix(target, "#") {
		return true
	}
	// Absolute URLs — anything with a scheme.
	if idx := strings.Index(target, "://"); idx > 0 && idx < 10 {
		return true
	}
	if strings.HasPrefix(target, "mailto:") {
		return true
	}
	return false
}

func byteOffsetToLine(data []byte, off int) int {
	line := 1
	for i := 0; i < off && i < len(data); i++ {
		if data[i] == '\n' {
			line++
		}
	}
	return line
}

// Run executes doc-gardener, prints findings to stderr, and returns an
// exit code. 0 = clean, 1 = findings, 2 = I/O error.
func Run(repoRoot string, stderr io.Writer) int {
	findings, err := CheckRepo(repoRoot)
	if err != nil {
		fmt.Fprintf(stderr, "doc-gardener: %v\n", err)
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
