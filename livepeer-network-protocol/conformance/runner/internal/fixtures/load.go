package fixtures

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadAll walks dir recursively, parses every *.yaml/*.yml file as a Fixture,
// and returns them sorted by path for deterministic ordering.
func LoadAll(dir string) ([]Fixture, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".yaml" || ext == ".yml" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %q: %w", dir, err)
	}
	sort.Strings(paths)

	out := make([]Fixture, 0, len(paths))
	for _, p := range paths {
		fx, err := load(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		out = append(out, fx)
	}
	return out, nil
}

func load(path string) (Fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, fmt.Errorf("read: %w", err)
	}
	var fx Fixture
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&fx); err != nil {
		return Fixture{}, fmt.Errorf("parse: %w", err)
	}
	fx.Path = path
	return fx, nil
}
