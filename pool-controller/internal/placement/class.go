// Package placement decides which workload templates run on which GPU.
//
// The inputs are all facts the pool already holds: the hardware a
// member's agent declared at attach, the templates this pool enabled,
// and what members opted out of. Nothing here asks an operator to
// choose — placement is policy applied to declared facts, which is what
// makes onboarding zero-touch (plan 0044 §3.3).
package placement

import (
	"regexp"
	"strings"
)

// A GPU class is the unit the pool's hardware policy is written in
// (plan 0040 §4.3, §4.4). Templates state which classes they may run
// on; members' cards report a driver marketing string. These are the
// classes that vocabulary uses.
const (
	ClassGTX1080 = "gtx-1080"
	ClassRTX2080 = "rtx-2080"
	ClassRTX3090 = "rtx-3090"
	ClassRTX4090 = "rtx-4090"
	ClassRTX5090 = "rtx-5090"
	ClassA100    = "a100"
	ClassH100    = "h100"
	ClassL40S    = "l40s"
	// ClassUnknown is a card the pool has no policy for. It is not an
	// error and not a rejection of the member — it is a card no
	// template has claimed it can use, which is the safe answer for
	// hardware this build has never been told about.
	ClassUnknown = ""
)

// laptopRE catches the mobile parts. A "RTX 4090 Laptop GPU" shares a
// marketing name with the desktop 4090 and almost nothing else — around
// two thirds of the memory and a different power envelope — so folding
// it into the desktop class would place workloads sized for 24GB onto a
// 16GB card. It gets no class rather than the wrong one.
var laptopRE = regexp.MustCompile(`(?i)\blaptop\b|\bmobile\b|\bmax-q\b`)

// modelPatterns map a driver string to a class. Order matters: the
// first match wins, so more specific patterns come first.
var modelPatterns = []struct {
	re    *regexp.Regexp
	class string
}{
	// Datacentre parts name themselves plainly and carry a memory
	// suffix that is not part of the class.
	{regexp.MustCompile(`(?i)\bh100\b`), ClassH100},
	{regexp.MustCompile(`(?i)\ba100\b`), ClassA100},
	{regexp.MustCompile(`(?i)\bl40s\b`), ClassL40S},
	// Consumer parts. Ti and SUPER variants sit in their base class:
	// the pool's policy is written per generation, and a 2080 Ti is a
	// 2080-class card for every purpose 0040 §4.4 cares about.
	{regexp.MustCompile(`(?i)\brtx\s*5090\b`), ClassRTX5090},
	{regexp.MustCompile(`(?i)\brtx\s*4090\b`), ClassRTX4090},
	{regexp.MustCompile(`(?i)\brtx\s*3090\b`), ClassRTX3090},
	{regexp.MustCompile(`(?i)\brtx\s*2080\b`), ClassRTX2080},
	{regexp.MustCompile(`(?i)\bgtx\s*1080\b`), ClassGTX1080},
}

// ClassOf normalises what a driver reported into a pool GPU class.
//
// The string comes from nvidia-smi by way of the member's agent and the
// attach document — "NVIDIA GeForce RTX 4090", "NVIDIA H100 80GB HBM3"
// — so this has to tolerate the vendor's prefixes and memory suffixes
// without matching on them.
func ClassOf(gpuModel string) string {
	model := strings.TrimSpace(gpuModel)
	if model == "" {
		return ClassUnknown
	}
	if laptopRE.MatchString(model) {
		return ClassUnknown
	}
	for _, pattern := range modelPatterns {
		if pattern.re.MatchString(model) {
			return pattern.class
		}
	}
	return ClassUnknown
}

// DefaultStances is how many templates a class tolerates at once
// (plan 0040 §4.4).
//
// The older consumer cards run one workload and nothing else: they have
// neither the memory headroom nor the scheduler to share without both
// tenants suffering. The 4090 and 5090 take a primary plus a
// low-footprint secondary — that is what stacking exists for. The
// datacentre parts are partitionable and take more.
//
// A class absent from this table is treated as single-template, which
// is the conservative reading for hardware the pool has no stance on.
var DefaultStances = map[string]int{
	ClassGTX1080: 1,
	ClassRTX2080: 1,
	ClassRTX3090: 1,
	ClassRTX4090: 2,
	ClassRTX5090: 2,
	ClassA100:    4,
	ClassH100:    4,
	ClassL40S:    2,
}

// MaxTemplatesFor reports the stacking stance for a class, applying the
// pool's overrides over the defaults.
func MaxTemplatesFor(class string, overrides map[string]int) int {
	if limit, ok := overrides[class]; ok && limit > 0 {
		return limit
	}
	if limit, ok := DefaultStances[class]; ok {
		return limit
	}
	return 1
}
