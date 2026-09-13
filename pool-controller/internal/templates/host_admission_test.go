package templates

import (
	"strings"
	"testing"
)

const hostAdmissionBase = imageMapBase + `runner_compose:
  image: { nvidia: x/y:1 }
`

func TestHostAdmissionValidation(t *testing.T) {
	valid := hostAdmissionBase + `  host_admission:
    mechanism: flock-files/v1
    scope: hardware-unit
    env_var: GPU_ADMISSION_LOCK
    file_suffixes: [.mutex, .live, .batch]
`
	if err := loadOne(t, valid); err != nil {
		t.Fatalf("valid host admission rejected: %v", err)
	}

	tests := []struct {
		name, block, want string
	}{
		{"mechanism", "    mechanism: other/v1\n    scope: hardware-unit\n    env_var: LOCK\n    file_suffixes: [.x]\n", "mechanism"},
		{"scope", "    mechanism: flock-files/v1\n    scope: capability\n    env_var: LOCK\n    file_suffixes: [.x]\n", "scope"},
		{"environment", "    mechanism: flock-files/v1\n    scope: hardware-unit\n    env_var: bad-name\n    file_suffixes: [.x]\n", "env_var"},
		{"path traversal", "    mechanism: flock-files/v1\n    scope: hardware-unit\n    env_var: LOCK\n    file_suffixes: [../../escape]\n", "unsafe suffix"},
		{"duplicate", "    mechanism: flock-files/v1\n    scope: hardware-unit\n    env_var: LOCK\n    file_suffixes: [.x, .x]\n", "repeats"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := loadOne(t, hostAdmissionBase+"  host_admission:\n"+tc.block)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestHostAdmissionEnvironmentCannotBeOverridden(t *testing.T) {
	err := loadOne(t, imageMapBase+`runner_compose:
  image: { nvidia: x/y:1 }
  env: { GPU_ADMISSION_LOCK: /tmp/wrong }
  host_admission:
    mechanism: flock-files/v1
    scope: hardware-unit
    env_var: GPU_ADMISSION_LOCK
    file_suffixes: [.mutex]
`)
	if err == nil || !strings.Contains(err.Error(), "must not override") {
		t.Fatalf("error = %v, want override rejection", err)
	}
}
