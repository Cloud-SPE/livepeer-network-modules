// Package gpu is the vocabulary two other packages have to agree on:
// which VENDOR a card belongs to, and which vendor each pool GPU class
// implies.
//
// It exists as its own package because the two places that need it
// cannot import each other. placement decides eligibility and owns the
// class table; templates validates a catalog and must refuse a template
// whose gpu_classes admit a vendor its image map cannot serve (plan 0045
// §4). placement already imports templates, so the vendor facts live
// below both.
//
// Vendor is what selects the runner image and the compose device block.
// A card is exposed to a container differently per vendor — NVIDIA by a
// per-card UUID through the nvidia runtime, Intel by the /dev/dri render
// nodes — so the vendor is load-bearing for rendering, not a label.
package gpu

import "strings"

// Vendors. Closed: a template naming any other key in its image map is
// a typo, and a card reporting any other prefix is a card the pool has
// no way to expose.
const (
	VendorNVIDIA = "nvidia"
	VendorIntel  = "intel"
	VendorAMD    = "amd"
	// VendorCPU is the image-map key for a cpu unit (plan 0047): not a
	// GPU vendor, but the same question — which build runs here.
	VendorCPU = "cpu"
	// VendorAny is the image-map fallback for a build that runs on any
	// unit — a software encoder, a media router — where the vendor
	// map's question ("which build runs here") has one answer.
	VendorAny = "any"
)

// Vendors lists the known image-map keys, sorted.
func Vendors() []string { return []string{VendorAMD, VendorAny, VendorCPU, VendorIntel, VendorNVIDIA} }

// Known reports whether v is a key the pool can render for.
func Known(v string) bool {
	switch v {
	case VendorNVIDIA, VendorIntel, VendorAMD, VendorCPU, VendorAny:
		return true
	}
	return false
}

// VendorOfModel reads the vendor off a driver-reported model string.
//
// Every driver puts its own name first — "NVIDIA GeForce RTX 4090",
// "Intel(R) Arc(TM) A770 Graphics", "AMD Radeon RX 7900 XTX" — so the
// prefix is reliable in a way the rest of the string is not. Empty when
// the string names no vendor the pool knows.
func VendorOfModel(gpuModel string) string {
	m := strings.ToLower(strings.TrimSpace(gpuModel))
	switch {
	case strings.HasPrefix(m, "nvidia"):
		return VendorNVIDIA
	case strings.HasPrefix(m, "intel"):
		return VendorIntel
	case strings.HasPrefix(m, "amd") || strings.HasPrefix(m, "advanced micro devices"):
		return VendorAMD
	}
	return ""
}

// classVendors is the vendor each pool GPU class implies. The class
// names are placement's (class.go); a class listed here and not there,
// or the reverse, is caught by placement's tests.
var classVendors = map[string]string{
	"gtx-1080": VendorNVIDIA,
	"rtx-2080": VendorNVIDIA,
	"rtx-3090": VendorNVIDIA,
	"rtx-4090": VendorNVIDIA,
	"rtx-5090": VendorNVIDIA,
	"a100":     VendorNVIDIA,
	"h100":     VendorNVIDIA,
	"l40s":     VendorNVIDIA,
	"arc-a770": VendorIntel,
	"arc-b580": VendorIntel,
	"flex-170": VendorIntel,
	// CPU core tiers (placement/class.go). One image key serves them all.
	"cpu-8": VendorCPU, "cpu-16": VendorCPU, "cpu-32": VendorCPU, "cpu-64": VendorCPU,
}

// VendorOfClass is the vendor a pool GPU class implies, or empty for a
// class the pool does not know — which a catalog may legitimately name
// ahead of placement learning it, so it is not an error here.
func VendorOfClass(class string) string {
	return classVendors[strings.ToLower(strings.TrimSpace(class))]
}

// Classes lists every class the pool knows a vendor for, sorted.
func Classes() []string {
	out := make([]string, 0, len(classVendors))
	for c := range classVendors {
		out = append(out, c)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
