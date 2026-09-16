package memberenrollment

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapSelectsOnlyHostInventoryRuntime(t *testing.T) {
	for _, tc := range []struct {
		name, vendor, class, smi string
		wantGPU, wantError       bool
	}{
		{"Intel", "0x8086", "0x030000", "exit 1", false, false},
		{"CPU", "0x8086", "0x060000", "exit 1", false, false},
		{"NVIDIA", "0x10de", "0x030000", "exit 0", true, false},
		{"broken NVIDIA driver", "0x10de", "0x030000", "exit 1", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pci := filepath.Join(dir, "pci")
			bin := filepath.Join(dir, "bin")
			for _, p := range []string{filepath.Join(pci, "0000:01:00.0"), bin} {
				if err := os.MkdirAll(p, 0700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, body string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(pci, "0000:01:00.0/vendor"), tc.vendor)
			write(filepath.Join(pci, "0000:01:00.0/class"), tc.class)
			write(filepath.Join(bin, "nvidia-smi"), "#!/bin/sh\n"+tc.smi+"\n")
			write(filepath.Join(bin, "docker"), "#!/bin/sh\nprintf '%s\\n' \"$*\" > docker-called\n")
			write(filepath.Join(dir, "start.sh"), strings.ReplaceAll(bundleStartScript(), "/sys/bus/pci/devices", pci))
			cmd := exec.Command("sh", "start.sh", "--force-recreate")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if (err != nil) != tc.wantError {
				t.Fatalf("%s: %v", out, err)
			}
			if tc.wantError {
				if _, err := os.Stat(filepath.Join(dir, "docker-called")); !os.IsNotExist(err) {
					t.Fatal("started with broken driver")
				}
				return
			}
			raw, err := os.ReadFile(filepath.Join(dir, "docker-compose.override.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "gpus: all") != tc.wantGPU {
				t.Fatalf("wrong runtime %s", raw)
			}
			called, _ := os.ReadFile(filepath.Join(dir, "docker-called"))
			if string(called) != "compose up -d --force-recreate pool_member_agent\n" {
				t.Fatalf("wrong launch: %s", called)
			}
		})
	}
}
