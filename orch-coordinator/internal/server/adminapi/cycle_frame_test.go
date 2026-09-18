package adminapi

import (
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/service/diff"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/service/roster"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/types"
)

func manifestAt(seq uint64) *types.ManifestPayload {
	now := time.Date(2026, 9, 7, 22, 0, 0, 0, time.UTC)
	return &types.ManifestPayload{
		SpecVersion: "2.4.1", PublicationSeq: seq, IssuedAt: now, ExpiresAt: now.Add(24 * time.Hour),
		Orch:           types.Orch{EthAddress: "0xd00354656922168815fcd1e51cbddb9e359e3c7f"},
		SettlementKeys: []types.SettlementKey{{PublicKey: "0x04aa", NotBefore: now, ExpiresAt: now.Add(365 * 24 * time.Hour)}},
		Capabilities: []types.CapabilityTuple{{
			CapabilityID: "openai:chat-completions", OfferingID: "qwen3.6-27b", Protocol: "paid-job/v1",
			WorkUnit: types.WorkUnit{Name: "tokens"}, PricePerUnitWei: "830000000000", WorkerURL: "https://ai2-rig-broker.xode.app",
		}},
	}
}

func noDrift() *roster.View {
	return &roster.View{DriftCounts: map[string]int{diff.DriftNone: 1}}
}

// After a publish the builder advances to the next sequence and rebuilds.
// That successor changes nothing, so the page must frame the cycle on the
// live manifest and report it complete — not six pending steps.
func TestCycleFrame_NoopSuccessorTracksPublishedManifest(t *testing.T) {
	pub := manifestAt(8)
	cand := manifestAt(9)
	frame := cycleFrame(cand, pub, noDrift())
	if frame.name != cycleFramePublished || frame.seq != 8 || frame.hash != manifestCanonicalHash(pub) {
		t.Fatalf("frame = %+v", frame)
	}
	events := []audit.Event{
		{Outcome: audit.OutcomeAccepted, ManifestSHA256: frame.hash, At: time.Now()},
		{Outcome: audit.OutcomeSignedReturned, ManifestSHA256: frame.hash, At: time.Now()},
		{Outcome: audit.OutcomeCandidateDownloaded, ManifestSHA256: frame.hash, At: time.Now()},
	}
	steps := coordinatorChecklist(frame.hash, events, cycleTimeline(events, frame.hash), "")
	for _, st := range steps {
		if st.Status == "pending" {
			t.Fatalf("step %q pending on a completed cycle", st.Label)
		}
	}
	state, title, note := assessCoordinatorCycle(true, 9, true, 8, true, true, true)
	if state != "ok" || !strings.Contains(title, "up to date") || !strings.Contains(note, "Publication 8") {
		t.Fatalf("stage = %s %q %q", state, title, note)
	}
}

// A candidate that changes something is still framed on itself, and its
// steps are pending until it is carried.
func TestCycleFrame_ChangedCandidateTracksItself(t *testing.T) {
	pub := manifestAt(8)
	cand := manifestAt(9)
	cand.Capabilities[0].PricePerUnitWei = "1"
	view := &roster.View{DriftCounts: map[string]int{diff.DriftPriceChanged: 1}}
	frame := cycleFrame(cand, pub, view)
	if frame.name != cycleFrameCandidate || frame.seq != 9 || frame.hash != manifestCanonicalHash(cand) {
		t.Fatalf("frame = %+v", frame)
	}
	if state, title, _ := assessCoordinatorCycle(true, 9, true, 8, false, false, false); state != "warn" || !strings.Contains(title, "Awaiting") {
		t.Fatalf("stage = %s %q", state, title)
	}

	// A delegation change alone is a change too, even with no tuple drift.
	keysOnly := manifestAt(9)
	keysOnly.SettlementKeys = append(keysOnly.SettlementKeys, types.SettlementKey{PublicKey: "0x04bb", NotBefore: keysOnly.IssuedAt, ExpiresAt: keysOnly.ExpiresAt})
	if f := cycleFrame(keysOnly, pub, noDrift()); f.name != cycleFrameCandidate {
		t.Fatalf("settlement key change framed as no-op: %+v", f)
	}
	// No published manifest: the first cycle is always the candidate's.
	if f := cycleFrame(cand, nil, noDrift()); f.name != cycleFrameCandidate || f.seq != 9 {
		t.Fatalf("first cycle: %+v", f)
	}
}

// A verified signature coming back is proof the cold-key host received,
// reviewed and signed the candidate: steps 2–4 flip from remote to done.
// Before that they stay remote, never pending.
func TestChecklist_RemoteStepsInferredFromReturnedSignature(t *testing.T) {
	hash := "sha256:abc"
	before := coordinatorChecklist(hash, []audit.Event{{Outcome: audit.OutcomeCandidateDownloaded, ManifestSHA256: hash}}, nil, "")
	for _, i := range []int{1, 2, 3} {
		if before[i].Status != "remote" || strings.Contains(before[i].Note, "--secure-orch-url") {
			t.Fatalf("before return, step %q = %s %q", before[i].Label, before[i].Status, before[i].Note)
		}
	}
	after := coordinatorChecklist(hash, []audit.Event{{Outcome: audit.OutcomeSignedReturned, ManifestSHA256: hash}}, nil, "https://secure.example")
	for _, i := range []int{1, 2, 3} {
		if after[i].Status != "done" || !strings.Contains(after[i].Note, "Inferred") || after[i].Href != "https://secure.example/manifests#review-timeline" {
			t.Fatalf("after return, step %q = %s %q %q", after[i].Label, after[i].Status, after[i].Note, after[i].Href)
		}
	}
	if after[0].Status != "pending" || after[4].Status != "done" || after[5].Status != "pending" {
		t.Fatalf("own steps: %s %s %s", after[0].Status, after[4].Status, after[5].Status)
	}
	if checklistHint("") == "" || checklistHint("https://secure.example") != "" {
		t.Fatal("hint must show only while the flag is unset")
	}
}
