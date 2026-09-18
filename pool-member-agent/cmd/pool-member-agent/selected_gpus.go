package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-member-agent/internal/attach"
)

func parseSelectedGPUs(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []string
	seen := map[string]bool{}
	valid := regexp.MustCompile(`^[a-z0-9][a-z0-9:._-]{0,255}$`)
	for _, device := range strings.Split(raw, ",") {
		device = strings.ToLower(strings.TrimSpace(device))
		if !valid.MatchString(device) || seen[device] {
			return nil, fmt.Errorf("POOL_GPU_UUIDS must contain unique GPU UUIDs")
		}
		seen[device] = true
		out = append(out, device)
	}
	return out, nil
}
func filterSelectedHardware(units []attach.Hardware, selection []string) []attach.Hardware {
	if len(selection) == 0 {
		return units
	}
	wanted := map[string]bool{}
	for _, device := range selection {
		wanted[strings.ToLower(device)] = true
	}
	var out []attach.Hardware
	for _, unit := range units {
		if wanted[strings.ToLower(unit.GPUUUID)] {
			out = append(out, unit)
		}
	}
	return out
}
