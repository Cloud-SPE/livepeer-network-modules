package types

import (
	"testing"
	"time"
)

func TestManifest_Clone_DeepCopy(t *testing.T) {
	m := &Manifest{
		SchemaVersion: SchemaVersion,
		EthAddress:    "0xabcdef0000000000000000000000000000000000",
		IssuedAt:      time.Now().UTC(),
		Nodes: []Node{
			{ID: "n1", URL: "https://x", Capabilities: []Capability{
				{Name: "c1", Offerings: []Offering{{ID: "m1"}}},
			}},
		},
	}
	c := m.Clone()
	c.Nodes[0].ID = "mutated"
	c.Nodes[0].Capabilities[0].Name = "changed"
	c.Nodes[0].Capabilities[0].Offerings[0].ID = "model-mutated"
	if m.Nodes[0].ID != "n1" {
		t.Fatalf("clone mutated original Node.ID")
	}
	if m.Nodes[0].Capabilities[0].Name != "c1" {
		t.Fatalf("clone mutated original Capability.Name")
	}
	if m.Nodes[0].Capabilities[0].Offerings[0].ID != "m1" {
		t.Fatalf("clone mutated original Offering.ID")
	}
}
