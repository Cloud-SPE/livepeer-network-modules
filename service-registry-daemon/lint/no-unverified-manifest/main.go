// Command no-unverified-manifest checks Go source for direct
// json.Unmarshal into types.Manifest, which would bypass the
// boundary-validating DecodeManifest. See lint/README.md.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	var problems []string
	_ = filepath.WalkDir(*root, func(path string, d fs.DirEntry, _ error) error {
		if d == nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Skip the decoder itself, and this lint's own source (which
		// references the bad pattern in its description).
		if strings.Contains(path, "internal/types/decoder.go") {
			return nil
		}
		if strings.Contains(path, "lint/no-unverified-manifest/") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		s := string(body)
		// Crude pattern-match: "json.Unmarshal" followed within ~80
		// chars by "Manifest{" or "*Manifest" or "&Manifest{".
		if !strings.Contains(s, "json.Unmarshal") {
			return nil
		}
		idx := strings.Index(s, "json.Unmarshal")
		for idx >= 0 {
			window := s[idx:min(len(s), idx+200)]
			if strings.Contains(window, "Manifest") {
				problems = append(problems, fmt.Sprintf("%s: json.Unmarshal into Manifest detected; use types.DecodeManifest instead", path))
				break
			}
			next := strings.Index(s[idx+1:], "json.Unmarshal")
			if next < 0 {
				break
			}
			idx = idx + 1 + next
		}
		return nil
	})

	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "no-unverified-manifest:", p)
		}
		os.Exit(1)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
