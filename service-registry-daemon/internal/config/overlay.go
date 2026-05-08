package config

import (
	"bytes"
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
	"gopkg.in/yaml.v3"
)

// Overlay is the parsed static-overlay file. See
// docs/design-docs/static-overlay.md.
type Overlay struct {
	Entries []OverlayEntry
	// Index from canonical eth-address to entry index in Entries, built
	// in BuildOverlay so resolver lookups are O(1).
	byAddr map[types.EthAddress]int
}

// OverlayEntry is one operator-curated record per orchestrator.
type OverlayEntry struct {
	EthAddress      types.EthAddress
	Enabled         bool
	TierAllowed     []string // nil = no tier filter
	Weight          int      // 1..1000; 0 means "default" → 100
	UnsignedAllowed bool
	Pin             []OverlayPinNode
}

// OverlayPinNode is a node the operator manages off-chain that should
// be injected into resolver results for this orchestrator.
type OverlayPinNode struct {
	ID           string
	URL          string
	Capabilities []types.Capability
	TierAllowed  []string
	Weight       int
}

// FindByAddress returns the overlay entry for an address (or nil) plus
// whether it was present. The bool helps callers distinguish "no entry"
// from "default-zero entry".
func (o *Overlay) FindByAddress(a types.EthAddress) (*OverlayEntry, bool) {
	if o == nil {
		return nil, false
	}
	idx, ok := o.byAddr[a]
	if !ok {
		return nil, false
	}
	return &o.Entries[idx], true
}

// rawOverlay mirrors the on-disk YAML shape exactly — this is the
// "parse at the boundary" layer. After parsing we copy into the
// validated Overlay struct above.
type rawOverlay struct {
	Overlay []rawEntry `yaml:"overlay"`
}

type rawEntry struct {
	EthAddress      string       `yaml:"eth_address"`
	Enabled         *bool        `yaml:"enabled"`
	TierAllowed     []string     `yaml:"tier_allowed"`
	Weight          *int         `yaml:"weight"`
	UnsignedAllowed *bool        `yaml:"unsigned_allowed"`
	Pin             []rawPinNode `yaml:"pin"`
}

type rawPinNode struct {
	ID           string             `yaml:"id"`
	URL          string             `yaml:"url"`
	Capabilities []rawPinCapability `yaml:"capabilities"`
	TierAllowed  []string           `yaml:"tier_allowed"`
	Weight       *int               `yaml:"weight"`
}

type rawPinCapability struct {
	Name      string           `yaml:"name"`
	WorkUnit  string           `yaml:"work_unit"`
	Offerings []rawPinOffering `yaml:"offerings"`
	Extra     map[string]any   `yaml:"extra"`
}

type rawPinOffering struct {
	ID                  string `yaml:"id"`
	PricePerWorkUnitWei string `yaml:"price_per_work_unit_wei"`
	Warm                *bool  `yaml:"warm"`
}

// ParseOverlayYAML decodes overlay YAML bytes into a validated *Overlay.
// Strict mode: unknown fields cause an error so operator typos are
// caught at config-load, not at request time.
func ParseOverlayYAML(raw []byte) (*Overlay, error) {
	if len(raw) == 0 {
		return &Overlay{byAddr: map[types.EthAddress]int{}}, nil
	}
	var ro rawOverlay
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&ro); err != nil {
		return nil, fmt.Errorf("overlay: parse: %w", err)
	}

	o := &Overlay{
		Entries: make([]OverlayEntry, 0, len(ro.Overlay)),
		byAddr:  make(map[types.EthAddress]int, len(ro.Overlay)),
	}
	for i, re := range ro.Overlay {
		addr, err := types.ParseEthAddress(re.EthAddress)
		if err != nil {
			return nil, fmt.Errorf("overlay[%d].eth_address: %w", i, err)
		}
		if _, dup := o.byAddr[addr]; dup {
			return nil, fmt.Errorf("overlay[%d]: duplicate eth_address %s", i, addr)
		}
		entry := OverlayEntry{
			EthAddress:      addr,
			Enabled:         true, // default
			TierAllowed:     append([]string(nil), re.TierAllowed...),
			Weight:          100,
			UnsignedAllowed: false,
		}
		if re.Enabled != nil {
			entry.Enabled = *re.Enabled
		}
		if re.Weight != nil {
			if *re.Weight < 1 || *re.Weight > 1000 {
				return nil, fmt.Errorf("overlay[%d].weight: must be 1..1000 (got %d)", i, *re.Weight)
			}
			entry.Weight = *re.Weight
		}
		if re.UnsignedAllowed != nil {
			entry.UnsignedAllowed = *re.UnsignedAllowed
		}
		for j, rp := range re.Pin {
			pn, err := convertPin(rp)
			if err != nil {
				return nil, fmt.Errorf("overlay[%d].pin[%d]: %w", i, j, err)
			}
			entry.Pin = append(entry.Pin, pn)
		}
		o.Entries = append(o.Entries, entry)
		o.byAddr[addr] = i
	}
	return o, nil
}

func convertPin(rp rawPinNode) (OverlayPinNode, error) {
	if rp.ID == "" {
		return OverlayPinNode{}, fmt.Errorf("id missing")
	}
	if rp.URL == "" {
		return OverlayPinNode{}, fmt.Errorf("url missing")
	}
	weight := 100
	if rp.Weight != nil {
		if *rp.Weight < 1 || *rp.Weight > 1000 {
			return OverlayPinNode{}, fmt.Errorf("weight out of range")
		}
		weight = *rp.Weight
	}
	caps := make([]types.Capability, 0, len(rp.Capabilities))
	for _, rc := range rp.Capabilities {
		if rc.Name == "" {
			return OverlayPinNode{}, fmt.Errorf("capability name missing")
		}
		c := types.Capability{Name: rc.Name, WorkUnit: rc.WorkUnit}
		for _, ro := range rc.Offerings {
			if ro.ID == "" {
				return OverlayPinNode{}, fmt.Errorf("capability %q offering id missing", rc.Name)
			}
			c.Offerings = append(c.Offerings, types.Offering{
				ID:                  ro.ID,
				PricePerWorkUnitWei: ro.PricePerWorkUnitWei,
			})
		}
		caps = append(caps, c)
	}
	return OverlayPinNode{
		ID:           rp.ID,
		URL:          rp.URL,
		Capabilities: caps,
		TierAllowed:  append([]string(nil), rp.TierAllowed...),
		Weight:       weight,
	}, nil
}

// EmptyOverlay returns a non-nil Overlay with no entries; callers can
// always call FindByAddress on it without nil checks.
func EmptyOverlay() *Overlay {
	return &Overlay{byAddr: map[types.EthAddress]int{}}
}
