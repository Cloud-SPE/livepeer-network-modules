package main

import (
	"github.com/Cloud-SPE/livepeer-network-modules/pool-member-agent/internal/attach"
	"testing"
)

func TestDestinationInventoryKeepsSourceSiblingOut(t *testing.T) {
	selected, err := parseSelectedGPUs(" GPU-B ")
	if err != nil {
		t.Fatal(err)
	}
	hardware := []attach.Hardware{{GPUUUID: "GPU-A"}, {GPUUUID: "GPU-B"}, {GPUUUID: "cpu-host"}}
	target := filterSelectedHardware(hardware, selected)
	if len(target) != 1 || target[0].GPUUUID != "GPU-B" {
		t.Fatalf("destination claimed sibling inventory %+v", target)
	}
	if got := filterSelectedHardware(hardware, nil); len(got) != 3 {
		t.Fatal("unselected discovery changed")
	}
	for _, raw := range []string{"gpu-a,GPU-A", "gpu-a,", "gpu-a\nOTHER=1", "../gpu"} {
		if _, err := parseSelectedGPUs(raw); err == nil {
			t.Fatalf("invalid selection accepted %q", raw)
		}
	}
}
