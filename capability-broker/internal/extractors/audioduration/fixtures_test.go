package audioduration

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

const fixtureDir = "../../../../livepeer-network-protocol/extractors/fixtures/multipart-audio-duration-v1"

type fixtureManifest struct {
	Estimator string `json:"estimator"`
	Rounding  string `json:"rounding"`
	Fixtures  []struct {
		File           string  `json:"file"`
		Format         string  `json:"format"`
		Seconds        float64 `json:"seconds"`
		CeilingSeconds int64   `json:"ceiling_seconds"`
		Reject         bool    `json:"reject"`
		Why            string  `json:"why"`
	} `json:"fixtures"`
}

// The shared fixtures are the contract between this implementation and
// any other — a client computing a funding ceiling before the work runs
// must reach the same number this reaches when it bills. Two parsers
// that disagree produce a ceiling the settlement then exceeds, and the
// disagreement surfaces as a refused exchange rather than a bug report.
//
// So this runs the published fixtures rather than its own copies of
// them. A change here that the fixtures do not cover is a change another
// implementation cannot know about.
func TestSharedFixtures(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(fixtureDir, "manifest.json"))
	if err != nil {
		t.Fatalf("shared fixtures unreadable: %v", err)
	}
	var m fixtureManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.Estimator != "multipart-audio-duration/v1" {
		t.Fatalf("manifest estimator = %q", m.Estimator)
	}
	if len(m.Fixtures) == 0 {
		t.Fatal("manifest declares no fixtures")
	}

	for _, f := range m.Fixtures {
		t.Run(f.File, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(fixtureDir, f.File))
			if err != nil {
				t.Fatal(err)
			}
			// The ESTIMATOR contract, which is the shared one: exact
			// ceiling or refusal. Probe is the broker-internal view and
			// reports inexact results for the billing path to apply
			// policy to; a funding ceiling has no such latitude.
			ceiling, err := EstimateCeilingSeconds(body)

			if f.Reject {
				if err == nil {
					t.Fatalf("accepted a fixture that must be refused (%s): got %ds",
						f.Why, ceiling)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a measurable fixture (%s): %v", f.Why, err)
			}
			if ceiling != f.CeilingSeconds {
				t.Fatalf("ceiling = %d; want %d", ceiling, f.CeilingSeconds)
			}
			// The raw duration and format are checked too, so a change
			// that happens to round to the same ceiling still shows up.
			res, perr := Probe(body)
			if perr != nil {
				t.Fatalf("probe: %v", perr)
			}
			if string(res.Format) != f.Format {
				t.Fatalf("format = %q; want %q", res.Format, f.Format)
			}
			if got := res.Duration.Seconds(); math.Abs(got-f.Seconds) > 1e-6 {
				t.Fatalf("seconds = %v; want %v", got, f.Seconds)
			}
		})
	}
}
