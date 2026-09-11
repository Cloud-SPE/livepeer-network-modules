package brokerpush

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
)

// loadShippedCatalog reads the catalog the pool actually ships. The
// recipes used to be hardcoded per capability in Go; they now live in
// these YAML files, so the file is where the assertions have to point.
func loadShippedCatalog(t *testing.T) []templates.Template {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "..", "templates")
	if _, err := os.Stat(dir); err != nil {
		// The catalog sits at the repo root, outside this module; a
		// checkout of pool-controller alone still tests fine.
		t.Skip("no repo-root templates/ directory alongside this module")
	}
	catalog, err := templates.Load(dir)
	if err != nil {
		t.Fatalf("templates.Load(%s) = %v", dir, err)
	}
	if len(catalog.All()) == 0 {
		t.Fatal("shipped catalog is empty")
	}
	return catalog.All()
}

// The step ORDER is a real constraint the template schema cannot
// express: a usage or latency step measures a request that has already
// been made, so one has to precede it.
func TestShippedCatalogCertificationIsOrdered(t *testing.T) {
	for _, tmpl := range loadShippedCatalog(t) {
		steps := certificationFor(tmpl)
		if len(steps) == 0 {
			t.Errorf("%s: no certification steps; an offer the broker cannot prove a runner serves should not be sold", tmpl.ID)
			continue
		}
		if steps[0].Type != "readiness" {
			t.Errorf("%s: first step is %q, want readiness — probing a runner that has not come up yet fails for the wrong reason", tmpl.ID, steps[0].Type)
		}
		sawRequest := false
		for _, step := range steps {
			if step.Type == "request" {
				sawRequest = true
			}
			if (step.Type == "usage" || step.Type == "latency") && !sawRequest {
				t.Errorf("%s: %s step has no preceding request to measure", tmpl.ID, step.Type)
			}
		}
	}
}

// Multipart families must not claim a JSON-shaped extractor's evidence:
// this is the heuristic the old probes.go got wrong, and it is now a
// property of the audio template rather than of a Go switch.
func TestShippedCatalogMultipartStepsCarryNoJSONBody(t *testing.T) {
	checked := 0
	for _, tmpl := range loadShippedCatalog(t) {
		for _, step := range certificationFor(tmpl) {
			if step.Config["transport"] != "multipart" {
				continue
			}
			checked++
			if _, hasBody := step.Config["body"]; hasBody {
				t.Errorf("%s step %q is multipart but carries a JSON body", tmpl.ID, step.Name)
			}
			if _, hasParts := step.Config["parts"]; !hasParts {
				t.Errorf("%s step %q is multipart but declares no parts", tmpl.ID, step.Name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no multipart step in the shipped catalog; this guard is watching nothing")
	}
}

// The goldens are the machine-readable copy of what the controller
// actually pushes, and `make check-certification-policy` validates them
// against the protocol's schema. They are generated from the templates
// through the same conversion the push uses, so a recipe the broker
// would reject is caught here rather than at a member's first attach.
func TestShippedCatalogCertificationGoldens(t *testing.T) {
	for _, tmpl := range loadShippedCatalog(t) {
		steps := certificationFor(tmpl)
		if len(steps) == 0 {
			continue
		}
		got, err := json.MarshalIndent(steps, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, '\n')
		path := filepath.Join("..", "..", "..", "testdata", "certification", tmpl.ID+".json")
		if os.Getenv("UPDATE_GOLDEN") == "1" {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run UPDATE_GOLDEN=1 go test ./... to write)", tmpl.ID, err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s drifted:\n got: %s\nwant: %s", tmpl.ID, got, want)
		}
	}
}

// Required is a pointer on the wire because the broker defaults an
// omitted one to true. Every shipped step must state it, or an
// intentionally-optional one arrives required.
func TestShippedCatalogAlwaysStatesRequired(t *testing.T) {
	var optional []string
	for _, tmpl := range loadShippedCatalog(t) {
		for _, step := range certificationFor(tmpl) {
			if step.Required == nil {
				t.Errorf("%s step %q left required unset", tmpl.ID, step.Name)
				continue
			}
			if !*step.Required {
				optional = append(optional, tmpl.ID+"/"+step.Name)
			}
		}
	}
	// A catalog where every step happens to be required would let the
	// omitted-defaults-to-true bug hide, so assert the case exists.
	if len(optional) == 0 {
		t.Fatal("no optional step in the shipped catalog; the required pointer's whole point is untested")
	}
}
