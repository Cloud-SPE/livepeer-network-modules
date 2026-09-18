package e2e

import (
	"net/http"
	"strings"
	"testing"
)

// TestShippedCatalogLoadsAndSells boots the controller on the catalog
// this build actually ships.
//
// The seam test above runs on a fixture so that editing a shipped
// template cannot make it red for an unrelated reason. But the shipped
// catalog is what every deployment gets, and it is a directory of
// hand-written YAML: a typo in it is a controller that boots with
// nothing to sell, or does not boot at all. Nothing else in the repo
// loads these files through the real binary.
func TestShippedCatalogLoadsAndSells(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two binaries")
	}
	p := startPool(t, repoCatalog(t))

	var catalog struct {
		Templates []struct {
			ID         string `json:"id"`
			Capability string `json:"capability"`
			OfferingID string `json:"offering_id"`
		} `json:"templates"`
	}
	status, raw := p.controller(http.MethodGet, "/admin/v1/template-catalog", "")
	if status != http.StatusOK {
		t.Fatalf("template catalog: %d %s", status, raw)
	}
	decode(t, raw, &catalog)
	if len(catalog.Templates) == 0 {
		t.Fatal("the controller booted with an empty catalog — either the directory is not shipped " +
			"or every file in it failed to parse")
	}

	// Offering ids reach the public registry and a buyer routes on
	// them. Two templates sharing one is two products sold under one
	// name, which no single-file review would catch.
	seen := map[string]string{}
	for _, tmpl := range catalog.Templates {
		if tmpl.OfferingID == "" || tmpl.Capability == "" {
			t.Errorf("%s: template is missing an offering id or capability", tmpl.ID)
		}
		if prior, dup := seen[tmpl.OfferingID]; dup {
			t.Errorf("%s and %s both claim offering id %q", prior, tmpl.ID, tmpl.OfferingID)
		}
		seen[tmpl.OfferingID] = tmpl.ID
	}

	// Every shipped template must survive being enabled and pushed. A
	// template that loads but cannot be turned into an offer the broker
	// accepts is one the operator finds broken at the moment they try
	// to sell it.
	for _, tmpl := range catalog.Templates {
		status, raw := p.controller(http.MethodPut, "/admin/v1/template-overrides/"+tmpl.ID,
			`{"enabled":true}`)
		if status != http.StatusOK {
			t.Fatalf("enable %s: %d %s", tmpl.ID, status, raw)
		}
	}
	status, raw = p.controller(http.MethodPost, "/admin/v1/reload", "")
	if status != http.StatusOK {
		t.Fatalf("reload: %d %s", status, raw)
	}

	var offers struct {
		Offers []struct {
			OfferingID string `json:"offering_id"`
		} `json:"offers"`
	}
	status, raw = p.broker(http.MethodGet, "/admin/v1/offers", "")
	if status != http.StatusOK {
		t.Fatalf("broker offers: %d %s", status, raw)
	}
	decode(t, raw, &offers)
	if len(offers.Offers) != len(catalog.Templates) {
		got := make([]string, 0, len(offers.Offers))
		for _, o := range offers.Offers {
			got = append(got, o.OfferingID)
		}
		t.Fatalf("the broker accepted %d of %d enabled templates; it took %s",
			len(offers.Offers), len(catalog.Templates), strings.Join(got, ", "))
	}
}
