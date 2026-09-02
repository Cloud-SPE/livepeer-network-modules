package main

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeIntelSysfs lays out /sys/class/drm the way i915 and xe do: a
// card directory whose device is a symlink into the PCI tree.
func fakeIntelSysfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mk := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	card := func(name, pci, vendor, device, driver string, lmem string, render string) {
		dev := filepath.Join(root, "devices", "pci0000:00", pci)
		mk(filepath.Join("devices", "pci0000:00", pci, "vendor"), vendor+"\n")
		mk(filepath.Join("devices", "pci0000:00", pci, "device"), device+"\n")
		if err := os.MkdirAll(filepath.Join(root, "bus", "pci", "drivers", driver), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, "bus", "pci", "drivers", driver), filepath.Join(dev, "driver")); err != nil {
			t.Fatal(err)
		}
		if render != "" {
			if err := os.MkdirAll(filepath.Join(dev, "drm", render), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		cardDir := filepath.Join(root, "class", "drm", name)
		if err := os.MkdirAll(cardDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(dev, filepath.Join(cardDir, "device")); err != nil {
			t.Fatal(err)
		}
		if lmem != "" {
			mk(filepath.Join("class", "drm", name, "lmem_total_bytes"), lmem+"\n")
		}
	}
	card("card0", "0000:03:00.0", "0x8086", "0x56a0", "i915", "17179869184", "renderD128") // Arc A770
	card("card1", "0000:00:02.0", "0x8086", "0x46a6", "i915", "", "renderD129")            // integrated UHD
	card("card2", "0000:04:00.0", "0x8086", "0xabcd", "xe", "8589934592", "renderD130")    // discrete, unknown id
	card("card3", "0000:05:00.0", "0x10de", "0x2684", "nvidia", "", "")                    // an NVIDIA card: not ours
	// A connector directory sits beside the cards and must be ignored.
	if err := os.MkdirAll(filepath.Join(root, "class", "drm", "card0-DP-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCollectIntelGPUsFromSysfs(t *testing.T) {
	units, err := collectIntelGPUs(fakeIntelSysfs(t), "host-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 {
		t.Fatalf("units = %+v, want the A770 and the unknown discrete card only", units)
	}
	a770 := units[0]
	if a770.GPUModel != "Intel Arc A770" || a770.VRAMBytes != 16<<30 || a770.Driver != "i915" {
		t.Fatalf("a770 = %+v", a770)
	}
	// The id is stable per slot and scoped to the host: a PCI address
	// alone would collide across members.
	if a770.GPUUUID != "intel-host-a-0000:03:00.0" {
		t.Fatalf("uuid = %q", a770.GPUUUID)
	}
	if a770.Facts["render_node"] != "/dev/dri/renderD128" || a770.Facts["pci_device_id"] != "0x56a0" || a770.Facts["source"] != "sysfs" {
		t.Fatalf("facts = %v", a770.Facts)
	}
	// Unknown discrete part: named by its id so placement's rejection
	// names it too, never invented into a class here.
	if units[1].GPUModel != "Intel GPU 0xabcd" || units[1].VRAMBytes != 8<<30 || units[1].Driver != "xe" {
		t.Fatalf("unknown = %+v", units[1])
	}
}

func TestCollectIntelGPUsOnAHostWithoutSysfs(t *testing.T) {
	units, err := collectIntelGPUs(t.TempDir(), "host-a")
	if err != nil || len(units) != 0 {
		t.Fatalf("units = %+v, err = %v; an absent tree is no GPUs, not an error", units, err)
	}
}
