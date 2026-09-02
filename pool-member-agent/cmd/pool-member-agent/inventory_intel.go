package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-member-agent/internal/attach"
)

// Intel hardware inventory (plan 0045 §4, lnm-3v7).
//
// nvidia-smi was the only inventory, so an Intel host attached with
// hardware: [] and was never placed — while the catalog, the placement
// classes, the per-vendor image map and the compose device block were
// all already waiting for it. There is no nvidia-smi for Intel that a
// member host is guaranteed to have (xpu-smi is a separate install), so
// this reads sysfs, which every kernel with i915 or xe exposes.
//
// What a unit needs (runner-attach §3.1): a stable id, the model as the
// driver reports it, memory. sysfs gives the PCI address and device id
// directly; the marketing name is not there, so it comes from a table
// of the ids the pool knows. An id the table does not know is reported
// as "Intel GPU 0x<id>" — placement rejects it as gpu_class_unknown
// naming that string, which is exactly decision 7's flow: a new card
// appears on the exception queue and becomes a one-line class when the
// operator wants it, rather than being invented here.

// intelVendorID is what sysfs reports for every Intel PCI device.
const intelVendorID = "0x8086"

// intelModels maps PCI device ids to the model string placement's
// class regexes expect (class.go: "\ba770\b", "\bb580\b", "flex 170").
// Best-effort and deliberately short: the discrete parts the pool has
// classes for, plus their siblings so a member's A750 is at least named.
var intelModels = map[string]string{
	"0x56a0": "Intel Arc A770", "0x56a1": "Intel Arc A750", "0x56a2": "Intel Arc A580",
	"0x56a5": "Intel Arc A380", "0x56a6": "Intel Arc A310",
	"0xe20b": "Intel Arc B580", "0xe20c": "Intel Arc B570",
	"0x56c0": "Intel Data Center GPU Flex 170", "0x56c1": "Intel Data Center GPU Flex 140",
}

var drmCardRE = regexp.MustCompile(`^card[0-9]+$`)

// collectIntelGPUs reads discrete Intel GPUs from a sysfs root. hostID
// is folded into the unit id because a PCI address is stable per slot
// but not unique across hosts, and the pool treats gpu_uuid collisions
// across members as a trust signal (plan 0040 §4.2).
func collectIntelGPUs(sysfsRoot, hostID string) ([]attach.Hardware, error) {
	cards, err := filepath.Glob(filepath.Join(sysfsRoot, "class", "drm", "card*"))
	if err != nil {
		return nil, err
	}
	var units []attach.Hardware
	for _, card := range cards {
		if !drmCardRE.MatchString(filepath.Base(card)) {
			continue // connectors: card0-DP-1 and friends
		}
		dev := filepath.Join(card, "device")
		if strings.TrimSpace(readFile(filepath.Join(dev, "vendor"))) != intelVendorID {
			continue
		}
		deviceID := strings.ToLower(strings.TrimSpace(readFile(filepath.Join(dev, "device"))))
		vram := intelLocalMemory(card)
		model, known := intelModels[deviceID]
		if !known {
			if vram == 0 {
				// An integrated GPU, almost always: no local memory
				// and not a part the pool sells for. Reporting it would
				// put a rejection on the exception queue for every
				// Intel laptop that joins.
				continue
			}
			model = "Intel GPU " + deviceID
		}
		pci := pciAddress(dev)
		facts := map[string]string{"source": "sysfs", "pci_address": pci, "pci_device_id": deviceID}
		if node := renderNode(dev); node != "" {
			facts["render_node"] = node
		}
		units = append(units, attach.Hardware{
			GPUUUID:   "intel-" + hostID + "-" + pci,
			GPUModel:  model,
			VRAMBytes: vram,
			Driver:    driverName(dev),
			Facts:     facts,
		})
	}
	return units, nil
}

// intelLocalMemory is the card's device memory, from whichever sysfs
// attribute the driver exposes; zero when none does, which placement
// treats as "unknown" for a vram floor and no template in the catalog
// sets one for Intel cards.
func intelLocalMemory(card string) uint64 {
	for _, rel := range []string{"lmem_total_bytes", "device/lmem_total_bytes", "device/mem_info_vram_total"} {
		if v, err := strconv.ParseUint(strings.TrimSpace(readFile(filepath.Join(card, rel))), 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return 0
}

// pciAddress is the device symlink's target name: 0000:03:00.0.
func pciAddress(dev string) string {
	if target, err := os.Readlink(dev); err == nil {
		return filepath.Base(target)
	}
	return filepath.Base(dev)
}

func driverName(dev string) string {
	if target, err := os.Readlink(filepath.Join(dev, "driver")); err == nil {
		return filepath.Base(target)
	}
	return ""
}

// renderNode is the /dev/dri/renderD* a container needs for this card.
// Reported as a fact for the operator; the compose device block mounts
// all of /dev/dri today (desiredstate.go) and would use this to pin.
func renderNode(dev string) string {
	nodes, _ := filepath.Glob(filepath.Join(dev, "drm", "renderD*"))
	if len(nodes) == 0 {
		return ""
	}
	return "/dev/dri/" + filepath.Base(nodes[0])
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// collectHardware is the inventory: every vendor's collector, each
// failing on its own. A host with no nvidia-smi is not a host with no
// GPUs, and the log line that used to say so was the reason an Intel
// host could never be placed.
func collectHardware(ctx context.Context, hostID string) []attach.Hardware {
	var hw []attach.Hardware
	if units, err := collectNVIDIAGPUs(ctx); err != nil {
		log.Printf("nvidia inventory unavailable (%v)", err)
	} else {
		hw = append(hw, units...)
	}
	if units, err := collectIntelGPUs("/sys", hostID); err != nil {
		log.Printf("intel inventory unavailable (%v)", err)
	} else {
		hw = append(hw, units...)
	}
	if len(hw) == 0 {
		// A CPU-only host is legitimate; GPU work simply will not match.
		log.Printf("no GPUs found; attaching with no hardware")
	}
	return hw
}
