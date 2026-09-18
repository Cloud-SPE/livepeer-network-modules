// Command coverage-gate enforces 75% statement coverage for every daemon package.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func main() {
	root := flag.String("root", ".", "component root")
	flag.Parse()
	if err := run(*root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(root string) error {
	required, err := runtimePackages(root)
	if err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(root, "coverage.out"))
	if err != nil {
		return err
	}
	defer f.Close()
	return check(f, required)
}
func runtimePackages(root string) (map[string]bool, error) {
	cmd := exec.Command("go", "list", "-json", "./cmd/...", "./internal/...")
	cmd.Dir = root
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("coverage inventory: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	required := map[string]bool{}
	for {
		var p struct {
			ImportPath, Dir string
			GoFiles         []string
		}
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		for _, name := range p.GoFiles {
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(p.Dir, name), nil, 0)
			if err != nil {
				return nil, err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				if body, ok := n.(*ast.BlockStmt); ok && len(body.List) > 0 {
					required[p.ImportPath] = true
				}
				return true
			})
		}
	}
	if len(required) == 0 {
		return nil, fmt.Errorf("coverage inventory is empty")
	}
	return required, nil
}

type counts struct{ total, covered int64 }

func check(r io.Reader, required map[string]bool) error {
	scan := bufio.NewScanner(r)
	if !scan.Scan() || !strings.HasPrefix(scan.Text(), "mode: ") {
		return fmt.Errorf("invalid coverage profile header")
	}
	totals := map[string]counts{}
	seen := map[string]bool{}
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) != 3 {
			return fmt.Errorf("malformed coverage record: %q", scan.Text())
		}
		pos := strings.LastIndex(fields[0], ":")
		if pos < 1 {
			return fmt.Errorf("missing coverage position")
		}
		n, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || n < 0 {
			return fmt.Errorf("invalid statement count")
		}
		hit, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || hit < 0 {
			return fmt.Errorf("invalid execution count")
		}
		if seen[fields[0]] {
			return fmt.Errorf("duplicate coverage block: %s", fields[0])
		}
		seen[fields[0]] = true
		pkg := path.Dir(fields[0][:pos])
		c := totals[pkg]
		c.total += n
		if hit > 0 {
			c.covered += n
		}
		totals[pkg] = c
	}
	if err := scan.Err(); err != nil {
		return err
	}
	var failures []string
	for pkg := range required {
		c, ok := totals[pkg]
		if !ok || c.total == 0 {
			failures = append(failures, pkg+": missing executable coverage")
			continue
		}
		if c.covered*100 < c.total*75 {
			failures = append(failures, fmt.Sprintf("%s: %.2f%% < 75%%", pkg, 100*float64(c.covered)/float64(c.total)))
		}
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return fmt.Errorf("coverage gate failed:\n%s", strings.Join(failures, "\n"))
	}
	return nil
}
