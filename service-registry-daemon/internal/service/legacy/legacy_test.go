package legacy

import (
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

func TestSynthesize(t *testing.T) {
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	n := Synthesize(addr, "https://orch.example.com:8935")
	if n.ID != "legacy" {
		t.Fatalf("ID = %s", n.ID)
	}
	if n.URL != "https://orch.example.com:8935" {
		t.Fatalf("URL = %s", n.URL)
	}
	if n.Capabilities != nil {
		t.Fatal("capabilities should be nil for legacy")
	}
	if n.SignatureStatus != types.SigLegacy {
		t.Fatalf("sig status = %v", n.SignatureStatus)
	}
	if n.Source != types.SourceLegacy {
		t.Fatalf("source = %v", n.Source)
	}
	if !n.Enabled {
		t.Fatal("legacy should default enabled=true")
	}
}
