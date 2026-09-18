package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadOne writes a single template and loads it, returning the error.
func loadOne(t *testing.T, body string) error {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	return err
}

const imageMapBase = `id: t
capability: video:transcode.vod
offering_id: default
protocol: paid-job/v1
price_default: { amount_wei: "1", per_units: 1 }
stacking: { primary: true }
`

// A template with no image for a vendor its class list admits would
// place a card on a workload it cannot pull. That has to fail at load,
// where it is a review failure, not on a member's host.
func TestImageMapMustCoverEveryAdmittedVendor(t *testing.T) {
	err := loadOne(t, imageMapBase+`requirements:
  gpu_classes: [rtx-4090, arc-a770]
runner_compose:
  image: { nvidia: x/y:1 }
`)
	if err == nil {
		t.Fatal("a class list admitting Intel with only an NVIDIA image was accepted")
	}
	if !strings.Contains(err.Error(), "arc-a770") || !strings.Contains(err.Error(), "intel") {
		t.Fatalf("error does not name the class and the missing vendor: %v", err)
	}

	// Both vendors covered: fine.
	if err := loadOne(t, imageMapBase+`requirements:
  gpu_classes: [rtx-4090, arc-a770]
runner_compose:
  image: { nvidia: x/y:1, intel: x/y-intel:1 }
`); err != nil {
		t.Fatalf("a fully covered map was rejected: %v", err)
	}
}

// A template that ships no runner at all is not gated — its images are
// unresolved, not wrong — so it keeps its class list.
func TestTemplateWithNoImageIsNotGatedOnVendor(t *testing.T) {
	if err := loadOne(t, imageMapBase+`requirements:
  gpu_classes: [rtx-4090, arc-a770]
`); err != nil {
		t.Fatalf("an image-less template was rejected: %v", err)
	}
}

// The vendor set is closed. A key the pool cannot render for is a typo.
func TestImageMapRejectsUnknownVendorAndEmptyImage(t *testing.T) {
	err := loadOne(t, imageMapBase+`runner_compose:
  image: { nvida: x/y:1 }
`)
	if err == nil || !strings.Contains(err.Error(), "nvida") {
		t.Fatalf("a misspelled vendor key was accepted: %v", err)
	}
	err = loadOne(t, imageMapBase+`runner_compose:
  image: { nvidia: "" }
`)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("an empty image was accepted: %v", err)
	}
}

// A scalar image is the old shape. It must be refused loudly rather
// than decoded into nothing: a template that quietly lost its image
// would place and render no service.
func TestImageMustBeAMapNotAScalar(t *testing.T) {
	err := loadOne(t, imageMapBase+`runner_compose:
  image: x/y:1
`)
	if err == nil {
		t.Fatal("a scalar image was accepted; it must be a per-vendor map")
	}
}

func TestImageForIsVendorKeyedAndCaseInsensitive(t *testing.T) {
	rc := RunnerCompose{Image: map[string]string{"nvidia": " x/y:1 "}}
	if got := rc.ImageFor("NVIDIA"); got != "x/y:1" {
		t.Fatalf("ImageFor(NVIDIA) = %q", got)
	}
	if got := rc.ImageFor("intel"); got != "" {
		t.Fatalf("ImageFor(intel) = %q, want empty for a vendor with no image", got)
	}
	if !rc.HasImage() || (RunnerCompose{}).HasImage() {
		t.Fatal("HasImage disagrees with the map")
	}
}
