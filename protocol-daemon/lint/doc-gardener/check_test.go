package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDoc drops src at relpath (relative to rootDir) creating parent dirs.
func writeDoc(t *testing.T, rootDir, relpath, src string) {
	t.Helper()
	p := filepath.Join(rootDir, relpath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestValidDesignDocAndPlanProduceNoFindings(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "docs/design-docs/architecture.md", `---
title: Architecture
status: verified
last-reviewed: 2026-04-24
---

# Architecture
Some text.
`)
	writeDoc(t, dir, "docs/exec-plans/completed/0001-scaffold.md", `---
id: 0001
slug: scaffold
title: Scaffold
status: completed
opened: 2026-04-01
started: 2026-04-01
completed: 2026-04-02
---

# Done
`)
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("want 0 findings on valid corpus, got: %+v", findings)
	}
}

func TestDesignDocMissingFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "docs/design-docs/a.md", "# no frontmatter here\n")
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}
	if len(findings) != 1 || findings[0].RuleID != "frontmatter" {
		t.Fatalf("want 1 frontmatter finding; got %+v", findings)
	}
}

func TestDesignDocBadStatus(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "docs/design-docs/a.md", `---
title: A
status: in-progress
last-reviewed: 2026-04-24
---
`)
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}
	if len(findings) != 1 || !strings.Contains(findings[0].Msg, "in-progress") {
		t.Fatalf("want status finding; got %+v", findings)
	}
}

func TestDesignDocBadDate(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "docs/design-docs/a.md", `---
title: A
status: accepted
last-reviewed: yesterday
---
`)
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}
	if len(findings) != 1 || !strings.Contains(findings[0].Msg, "YYYY-MM-DD") {
		t.Fatalf("want date finding; got %+v", findings)
	}
}

func TestIndexMdSkipsFrontmatterRequirement(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "docs/design-docs/index.md", "# Index\n\nJust a listing.\n")
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("index.md without frontmatter should not be flagged; got %+v", findings)
	}
}

func TestPlanStatusMismatchWithDir(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "docs/exec-plans/active/0001.md", `---
id: 0001
slug: x
title: X
status: completed
opened: 2026-04-24
---
`)
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}
	if len(findings) != 1 || !strings.Contains(findings[0].Msg, "does not match directory") {
		t.Fatalf("want mismatched-status finding; got %+v", findings)
	}
}

func TestCompletedPlanMissingCompletedDate(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "docs/exec-plans/completed/0001.md", `---
id: 0001
slug: x
title: X
status: completed
opened: 2026-04-01
---
`)
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}
	var hasCompletedReq bool
	for _, f := range findings {
		if strings.Contains(f.Msg, "completed (or closed) date") {
			hasCompletedReq = true
		}
	}
	if !hasCompletedReq {
		t.Errorf("want a finding requiring completed/closed; got %+v", findings)
	}
}

func TestClosedAliasAcceptedInCompletedPlan(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "docs/exec-plans/completed/0001.md", `---
id: 0001
slug: x
title: X
status: completed
opened: 2026-04-01
closed: 2026-04-02
---
`)
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("closed: should satisfy completed-plan requirement; got %+v", findings)
	}
}

func TestFencedCodeBlocksSkipped(t *testing.T) {
	dir := t.TempDir()
	// [text](fake.md) inside triple-backticks shouldn't be followed.
	writeDoc(t, dir, "docs/design-docs/a.md", "---\ntitle: A\nstatus: accepted\nlast-reviewed: 2026-04-24\n---\n\n```\nSee [x](fake.md) for example.\n```\n")
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("links inside code fences must be skipped; got %+v", findings)
	}
}

func TestInlineCodeSpanSkipped(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "docs/design-docs/a.md", "---\ntitle: A\nstatus: accepted\nlast-reviewed: 2026-04-24\n---\n\nSyntax: `[text](fake.md)` — example only.\n")
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("links inside inline code spans must be skipped; got %+v", findings)
	}
}

func TestBrokenLinkDetected(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "docs/design-docs/a.md", `---
title: A
status: accepted
last-reviewed: 2026-04-24
---

See [missing](./does-not-exist.md) for details.
`)
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}
	var gotBroken bool
	for _, f := range findings {
		if f.RuleID == "broken-link" {
			gotBroken = true
		}
	}
	if !gotBroken {
		t.Errorf("want broken-link finding; got %+v", findings)
	}
}

func TestLinksToExistingFilePass(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "docs/design-docs/a.md", `---
title: A
status: accepted
last-reviewed: 2026-04-24
---

See [core](core-beliefs.md).
`)
	writeDoc(t, dir, "docs/design-docs/core-beliefs.md", `---
title: Core
status: accepted
last-reviewed: 2026-04-24
---
`)
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("want 0 findings with valid links; got %+v", findings)
	}
}

func TestExternalAndAnchorLinksSkipped(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "docs/design-docs/a.md", `---
title: A
status: accepted
last-reviewed: 2026-04-24
---

Ext [ref](https://example.com/x), [mail](mailto:x@y.z), [anchor](#section), [empty]().
`)
	findings, err := CheckRepo(dir)
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("external/anchor/empty links should be skipped; got %+v", findings)
	}
}

func TestShouldSkipLink(t *testing.T) {
	cases := map[string]bool{
		"":               true,
		"#sec":           true,
		"https://ex.com": true,
		"http://ex.com":  true,
		"mailto:x@y":     true,
		"./a.md":         false,
		"../docs/b.md":   false,
	}
	for target, wantSkip := range cases {
		if got := shouldSkipLink(target); got != wantSkip {
			t.Errorf("shouldSkipLink(%q) = %v, want %v", target, got, wantSkip)
		}
	}
}

func TestByteOffsetToLine(t *testing.T) {
	src := []byte("a\nb\nc")
	cases := map[int]int{0: 1, 2: 2, 4: 3}
	for off, wantLine := range cases {
		if got := byteOffsetToLine(src, off); got != wantLine {
			t.Errorf("byteOffsetToLine(off=%d) = %d, want %d", off, got, wantLine)
		}
	}
}

func TestRunClean(t *testing.T) {
	dir := t.TempDir()
	// no docs/ dir → Run returns 0 (nothing to check)
	var buf bytes.Buffer
	if code := Run(dir, &buf); code != 0 {
		t.Errorf("want 0 on empty repo, got %d", code)
	}
}

func TestRunFindings(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "docs/design-docs/a.md", `no frontmatter`)
	var buf bytes.Buffer
	if code := Run(dir, &buf); code != 1 {
		t.Errorf("want 1 on findings, got %d", code)
	}
	if !strings.Contains(buf.String(), "frontmatter") {
		t.Errorf("stderr should mention frontmatter; got %q", buf.String())
	}
}

func TestFindingString(t *testing.T) {
	f := Finding{Path: "a.md", Line: 3, RuleID: "broken-link", Msg: "target X missing"}
	s := f.String()
	if !strings.Contains(s, "a.md:3: broken-link") || !strings.Contains(s, "Remediation:") {
		t.Errorf("finding format wrong: %q", s)
	}
}
