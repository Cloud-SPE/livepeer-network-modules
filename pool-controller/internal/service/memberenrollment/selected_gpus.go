package memberenrollment

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var selectedGPUName = regexp.MustCompile(`^[a-z0-9][a-z0-9:._-]{0,255}$`)

func normalizeSelectedGPUs(devices []string) ([]string, error) {
	if len(devices) > 64 {
		return nil, fmt.Errorf("at most 64 selected GPUs per enrollment")
	}
	var out []string
	seen := map[string]bool{}
	for _, device := range devices {
		device = strings.ToLower(strings.TrimSpace(device))
		if !selectedGPUName.MatchString(device) || seen[device] {
			return nil, fmt.Errorf("selected GPU UUIDs must be valid and unique")
		}
		seen[device] = true
		out = append(out, device)
	}
	sort.Strings(out)
	return out, nil
}
