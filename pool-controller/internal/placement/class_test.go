package placement

import "testing"

// ClassOf is the only place the pool touches a string it did not write:
// nvidia-smi's marketing name, relayed by the member's agent. Every
// hardware policy in the catalog is expressed in the classes this
// function produces, so a wrong answer here is a wrong placement on a
// real member's card.
func TestClassOfRealDriverStrings(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		// The forms nvidia-smi actually reports, prefix and memory
		// suffix included.
		{"desktop 4090", "NVIDIA GeForce RTX 4090", ClassRTX4090},
		{"desktop 3090", "NVIDIA GeForce RTX 3090", ClassRTX3090},
		{"datacentre h100", "NVIDIA H100 80GB HBM3", ClassH100},
		{"gtx 1080", "NVIDIA GeForce GTX 1080", ClassGTX1080},
		{"a100 with sxm suffix", "NVIDIA A100-SXM4-40GB", ClassA100},
		{"l40s", "NVIDIA L40S", ClassL40S},
		{"5090", "NVIDIA GeForce RTX 5090", ClassRTX5090},

		// Ti and SUPER are the same generation and the same policy, so
		// they belong to the base class rather than to none.
		{"2080 Ti", "NVIDIA GeForce RTX 2080 Ti", ClassRTX2080},
		{"2080 SUPER", "NVIDIA GeForce RTX 2080 SUPER", ClassRTX2080},
		{"3090 Ti", "NVIDIA GeForce RTX 3090 Ti", ClassRTX3090},
		{"1080 Ti", "NVIDIA GeForce GTX 1080 Ti", ClassGTX1080},

		// Tolerance: the string is relayed through an agent and a JSON
		// document, and operators type these into overrides by hand.
		{"lowercase", "nvidia geforce rtx 4090", ClassRTX4090},
		{"no space in model number", "RTX4090", ClassRTX4090},
		{"extra internal spacing", "NVIDIA   GeForce   RTX   3090", ClassRTX3090},
		{"surrounding whitespace", "  NVIDIA GeForce RTX 4090  ", ClassRTX4090},

		// Unknown hardware is not an error and not a rejection of the
		// member: it is a card no template has claimed it can use.
		{"empty", "", ClassUnknown},
		{"whitespace only", "   ", ClassUnknown},
		{"unlisted consumer card", "NVIDIA GeForce RTX 4060", ClassUnknown},
		{"unlisted workstation card", "NVIDIA RTX A4000", ClassUnknown},
		{"not a gpu at all", "Intel(R) UHD Graphics 630", ClassUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassOf(tc.model); got != tc.want {
				t.Fatalf("ClassOf(%q) = %q, want %q", tc.model, got, tc.want)
			}
		})
	}
}

// A mobile part shares a marketing name with the desktop card and about
// two thirds of its memory. Classing an "RTX 4090 Laptop GPU" as
// rtx-4090 would hand it a template sized for a 24GB card on 16GB of
// VRAM — the workload would OOM on a member's machine, and the member
// would have no way to see why. No class is the correct answer: the
// card simply runs nothing until the pool has a policy for it.
func TestClassOfRejectsMobileParts(t *testing.T) {
	mobile := []string{
		"NVIDIA GeForce RTX 4090 Laptop GPU",
		"NVIDIA GeForce RTX 3090 Laptop GPU",
		"NVIDIA GeForce RTX 4090 Max-Q",
		"NVIDIA GeForce RTX 2080 with Max-Q Design",
		"NVIDIA GeForce GTX 1080 Mobile",
		"nvidia geforce rtx 4090 laptop gpu",
	}
	for _, model := range mobile {
		t.Run(model, func(t *testing.T) {
			if got := ClassOf(model); got != ClassUnknown {
				t.Fatalf("ClassOf(%q) = %q, want ClassUnknown — a mobile part must never inherit the desktop class", model, got)
			}
		})
	}
}

// The stance is what stops the pool overcommitting a member's card. An
// unknown class must fall to one template, not to "no limit".
func TestMaxTemplatesFor(t *testing.T) {
	if got := MaxTemplatesFor(ClassRTX4090, nil); got != 2 {
		t.Errorf("MaxTemplatesFor(rtx-4090, nil) = %d, want 2 (0040 §4.4: primary plus low-footprint secondary)", got)
	}
	if got := MaxTemplatesFor(ClassGTX1080, nil); got != 1 {
		t.Errorf("MaxTemplatesFor(gtx-1080, nil) = %d, want 1", got)
	}
	if got := MaxTemplatesFor(ClassUnknown, nil); got != 1 {
		t.Errorf("MaxTemplatesFor(unknown, nil) = %d, want 1 — hardware the pool has no stance on gets the conservative one", got)
	}
	if got := MaxTemplatesFor("rtx-6090", nil); got != 1 {
		t.Errorf("MaxTemplatesFor(unlisted class, nil) = %d, want 1", got)
	}
	// An operator override is the whole point of the stances map: it is
	// how a pool loosens or tightens stacking without a rebuild.
	if got := MaxTemplatesFor(ClassRTX3090, map[string]int{ClassRTX3090: 2}); got != 2 {
		t.Errorf("MaxTemplatesFor(rtx-3090, {rtx-3090:2}) = %d, want the override 2", got)
	}
	if got := MaxTemplatesFor(ClassRTX4090, map[string]int{ClassRTX4090: 1}); got != 1 {
		t.Errorf("MaxTemplatesFor(rtx-4090, {rtx-4090:1}) = %d, want the override 1", got)
	}
	// A non-positive override is ignored rather than obeyed: zero would
	// otherwise read as "this class may run nothing", which placement
	// cannot express — a primary is placed before the limit is
	// consulted at all.
	if got := MaxTemplatesFor(ClassRTX4090, map[string]int{ClassRTX4090: 0}); got != 2 {
		t.Errorf("MaxTemplatesFor(rtx-4090, {rtx-4090:0}) = %d, want the default 2", got)
	}
}
