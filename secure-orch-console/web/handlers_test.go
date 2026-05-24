package web

import (
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/audit"
)

func TestSecureOrchChecklist_RemoteLinks(t *testing.T) {
	events := []cycleEventView{
		{Anchor: "cycle-1", Kind: string(audit.KindLoadCandidate)},
		{Anchor: "cycle-2", Kind: string(audit.KindViewDiff)},
		{Anchor: "cycle-3", Kind: string(audit.KindSign)},
	}

	steps := secureOrchChecklist(events, "https://coord.example.com/")
	if got := steps[0].Href; got != "https://coord.example.com/roster#cycle-timeline" {
		t.Fatalf("candidate downloaded href = %q", got)
	}
	if got := steps[1].Href; got != "#cycle-1" {
		t.Fatalf("load candidate href = %q", got)
	}
	if got := steps[2].Href; got != "#cycle-2" {
		t.Fatalf("view diff href = %q", got)
	}
	if got := steps[3].Href; got != "#cycle-3" {
		t.Fatalf("sign href = %q", got)
	}
	if !strings.Contains(steps[0].Note, "canonical_sha256") {
		t.Fatalf("expected canonical hash reminder in remote note, got %q", steps[0].Note)
	}
}

func TestSecureOrchChecklist_RemoteLinksDisabledWithoutCoordinatorURL(t *testing.T) {
	steps := secureOrchChecklist(nil, "")
	for _, idx := range []int{0, 4, 5} {
		if steps[idx].Href != "" {
			t.Fatalf("step %d href = %q, want empty", idx, steps[idx].Href)
		}
		if !strings.Contains(steps[idx].Note, "--coordinator-url") {
			t.Fatalf("step %d note = %q", idx, steps[idx].Note)
		}
	}
}
