// Command doc-gardener checks the repo's docs/ tree for staleness and
// broken cross-links. See lint/README.md.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	var problems []string
	docsDir := filepath.Join(*root, "docs")
	if _, err := os.Stat(docsDir); err != nil {
		fmt.Fprintf(os.Stderr, "doc-gardener: docs/ not found at %s\n", docsDir)
		os.Exit(1)
	}

	// 1. Walk docs/design-docs/, check frontmatter on each .md.
	designDir := filepath.Join(docsDir, "design-docs")
	_ = filepath.WalkDir(designDir, func(path string, d fs.DirEntry, _ error) error {
		if d == nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		if filepath.Base(path) == "index.md" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: read: %v", path, err))
			return nil
		}
		problems = append(problems, checkFrontmatter(path, string(body))...)
		return nil
	})

	problems = append(problems, checkCurrentLinks(*root)...)

	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "doc-gardener:", p)
		}
		os.Exit(1)
	}
}

// checkCurrentLinks checks every current Markdown file, including examples and
// lint instructions. Historical references and completed plans retain their
// original relative paths and are deliberately outside this current-doc gate.
func checkCurrentLinks(root string) []string {
	var problems []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "docs/references" || rel == "docs/exec-plans/completed" || strings.HasPrefix(d.Name(), ".") && rel != "." || d.Name() == "node_modules" || d.Name() == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		problems = append(problems, checkLinks(path, root, string(body))...)
		return nil
	})
	if err != nil {
		problems = append(problems, fmt.Sprintf("%s: walk: %v", root, err))
	}
	return problems
}

var (
	frontmatterRE = regexp.MustCompile(`(?s)^---\n(.*?)\n---\n`)
	statusRE      = regexp.MustCompile(`(?m)^status:\s*([a-z]+)\s*$`)
	titleRE       = regexp.MustCompile(`(?m)^title:\s*(.+?)\s*$`)
	reviewedRE    = regexp.MustCompile(`(?m)^last-reviewed:\s*(\S+)\s*$`)
	mdLinkRE      = regexp.MustCompile(`\[[^\]]+\]\(([^)#]+?)(#[^)]*)?\)`)
)

func checkFrontmatter(path, body string) []string {
	var out []string
	m := frontmatterRE.FindStringSubmatch(body)
	if m == nil {
		out = append(out, path+": missing frontmatter")
		return out
	}
	fm := m[1]
	if !titleRE.MatchString(fm) {
		out = append(out, path+": frontmatter missing title:")
	}
	stat := statusRE.FindStringSubmatch(fm)
	if stat == nil {
		out = append(out, path+": frontmatter missing status:")
	} else {
		switch stat[1] {
		case "proposed", "accepted", "verified", "deprecated":
		default:
			out = append(out, path+": invalid status: "+stat[1])
		}
	}
	rev := reviewedRE.FindStringSubmatch(fm)
	if rev == nil {
		out = append(out, path+": frontmatter missing last-reviewed:")
	} else {
		t, err := time.Parse("2006-01-02", strings.TrimSpace(rev[1]))
		if err != nil {
			out = append(out, path+": last-reviewed not YYYY-MM-DD: "+rev[1])
		} else if time.Since(t) > 365*24*time.Hour {
			out = append(out, path+": last-reviewed > 365 days old; review and bump")
		}
	}
	return out
}

func checkLinks(path, root, body string) []string {
	var out []string
	dir := filepath.Dir(path)
	for _, m := range mdLinkRE.FindAllStringSubmatch(body, -1) {
		target := strings.TrimSpace(m[1])
		// Skip absolute URLs and mailto:.
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") {
			continue
		}
		// Resolve relative to the file.
		full := filepath.Join(dir, target)
		if _, err := os.Stat(full); err != nil {
			out = append(out, fmt.Sprintf("%s: broken link -> %s", path, target))
		}
		_ = root
	}
	return out
}
