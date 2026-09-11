package main

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-member-agent/internal/attach"
)

// CPU inventory (plan 0047, lnm-iqn).
//
// A socket is a compute unit the pool can place on — SVT-AV1 on CPU is
// the better AV1 VOD encoder, and a member with cores and no card
// could never earn while the only placeable thing was a GPU. Read from
// /proc/cpuinfo, which every Linux host has: one unit per physical
// socket, physical cores counted by distinct (physical id, core id),
// threads by processors, the ISA from the flags the pool selects on.
//
// Always reported. Placement admits a cpu unit only to a template that
// lists cpu_classes, so a host with no CPU workload in the catalog
// gets a rejection on the exception queue for the CPU templates only —
// which is the truthful state — and its GPUs are unaffected.

// isaFlags maps cpuinfo flag names to the pool's ISA vocabulary.
var isaFlags = map[string]string{"avx2": "avx2", "avx512f": "avx512", "amx_tile": "amx"}

type cpuSocket struct {
	model   string
	cores   map[string]bool
	threads int
	isa     map[string]bool
}

// collectCPUs parses a cpuinfo file into one unit per socket.
func collectCPUs(procRoot, hostID string) ([]attach.Hardware, error) {
	f, err := os.Open(filepath.Join(procRoot, "cpuinfo"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sockets := map[string]*cpuSocket{}
	var order []string
	cur := map[string]string{}
	flush := func() {
		if len(cur) == 0 {
			return
		}
		phys := cur["physical id"]
		if phys == "" {
			phys = "0"
		}
		sk, ok := sockets[phys]
		if !ok {
			sk = &cpuSocket{model: cur["model name"], cores: map[string]bool{}, isa: map[string]bool{}}
			sockets[phys] = sk
			order = append(order, phys)
		}
		core := cur["core id"]
		if core == "" {
			core = cur["processor"]
		}
		sk.cores[core] = true
		sk.threads++
		for _, flag := range strings.Fields(cur["flags"]) {
			if name, ok := isaFlags[flag]; ok {
				sk.isa[name] = true
			}
		}
		cur = map[string]string{}
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		cur[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	flush()
	if err := sc.Err(); err != nil {
		return nil, err
	}
	units := make([]attach.Hardware, 0, len(order))
	for _, phys := range order {
		sk := sockets[phys]
		isa := make([]string, 0, len(sk.isa))
		for name := range sk.isa {
			isa = append(isa, name)
		}
		sort.Strings(isa)
		model := sk.model
		if model == "" {
			model = "unknown cpu"
		}
		units = append(units, attach.Hardware{
			GPUUUID:  "cpu-" + hostID + "-" + phys,
			GPUModel: model,
			Kind:     "cpu",
			Cores:    len(sk.cores),
			Threads:  sk.threads,
			ISA:      isa,
			Facts:    map[string]string{"source": "cpuinfo", "socket": phys, "threads": strconv.Itoa(sk.threads)},
		})
	}
	return units, nil
}
