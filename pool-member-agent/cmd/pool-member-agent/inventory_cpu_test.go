package main

import (
	"os"
	"path/filepath"
	"testing"
)

const twoSocketCPUInfo = `processor	: 0
model name	: AMD EPYC 9354 32-Core Processor
physical id	: 0
core id		: 0
flags		: fpu sse4_2 avx avx2 avx512f avx512bw

processor	: 1
model name	: AMD EPYC 9354 32-Core Processor
physical id	: 0
core id		: 0
flags		: fpu sse4_2 avx avx2 avx512f avx512bw

processor	: 2
model name	: AMD EPYC 9354 32-Core Processor
physical id	: 0
core id		: 1
flags		: fpu sse4_2 avx avx2 avx512f avx512bw

processor	: 3
model name	: AMD EPYC 9354 32-Core Processor
physical id	: 1
core id		: 0
flags		: fpu sse4_2 avx avx2

`

// One unit per socket, cores by distinct core id (hyperthreads do not
// double the count), the ISA from flags in the pool's vocabulary.
func TestCollectCPUsFromCPUInfo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cpuinfo"), []byte(twoSocketCPUInfo), 0o644); err != nil {
		t.Fatal(err)
	}
	units, err := collectCPUs(root, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 {
		t.Fatalf("units = %+v, want one per socket", units)
	}
	s0 := units[0]
	if s0.Kind != "cpu" || s0.GPUUUID != "cpu-host-a-0" || s0.GPUModel != "AMD EPYC 9354 32-Core Processor" ||
		s0.Cores != 2 || s0.Threads != 3 || len(s0.ISA) != 2 || s0.ISA[0] != "avx2" || s0.ISA[1] != "avx512" {
		t.Fatalf("socket 0 = %+v", s0)
	}
	if units[1].GPUUUID != "cpu-host-a-1" || units[1].Cores != 1 || len(units[1].ISA) != 1 {
		t.Fatalf("socket 1 = %+v", units[1])
	}
}
