package templates

import (
	"os"
	"path/filepath"
	"testing"
)

// The shipped catalog must load. It is the pool's actual product list,
// and a template that fails validation would take the controller down
// at boot rather than at review time.
func TestShippedCatalogLoads(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "templates")
	if _, err := os.Stat(dir); err != nil {
		// The catalog sits at the repo root, outside this module. A
		// checkout of pool-controller alone is still a valid thing to
		// test; it just has no catalog to check.
		t.Skip("no repo-root templates/ directory alongside this module")
	}
	cat, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(%s) = %v", dir, err)
	}
	if cat.Len() == 0 {
		t.Fatalf("shipped catalog is empty")
	}
	for _, tmpl := range cat.All() {
		if err := tmpl.Validate(); err != nil {
			t.Errorf("template %s: %v", tmpl.ID, err)
		}
		// Every template must be certifiable: an offer the broker
		// cannot prove a runner serves is one the pool should not sell.
		if len(tmpl.Certification) == 0 {
			t.Errorf("template %s declares no certification steps", tmpl.ID)
		}
	}
	t.Logf("loaded %d templates", cat.Len())
}
