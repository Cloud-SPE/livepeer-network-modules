package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/lastsigned"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/policy"
)

// HeldItem is the single held-for-operator slot (plan 0042 §8): a
// newer candidate superseding a held one replaces it, so the operator
// always reviews the latest diff, never a stale one.
type HeldItem struct {
	ETag            string           `json:"etag"`
	HeldAt          time.Time        `json:"held_at"`
	PublicationSeq  uint64           `json:"publication_seq"`
	CanonicalSHA256 string           `json:"canonical_sha256"`
	Class           string           `json:"class"`
	ShadowAutoSign  bool             `json:"would_auto_sign"`
	Findings        []policy.Finding `json:"findings"`
}

// HeldQueue persists the held candidate under dir as candidate.json
// (the manifest bytes the operator signs) + report.json (the
// classification report the UI renders).
type HeldQueue struct {
	Dir string
}

func (h *HeldQueue) candidatePath() string { return filepath.Join(h.Dir, "candidate.json") }
func (h *HeldQueue) reportPath() string    { return filepath.Join(h.Dir, "report.json") }

// Current returns the held item and candidate bytes, or (nil, nil,
// nil) when the queue is empty.
func (h *HeldQueue) Current() (*HeldItem, []byte, error) {
	report, err := os.ReadFile(h.reportPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("agent: read held report: %w", err)
	}
	var item HeldItem
	if err := json.Unmarshal(report, &item); err != nil {
		return nil, nil, fmt.Errorf("agent: parse held report: %w", err)
	}
	cand, err := os.ReadFile(h.candidatePath())
	if err != nil {
		return nil, nil, fmt.Errorf("agent: read held candidate: %w", err)
	}
	return &item, cand, nil
}

// Put stores (or replaces) the held candidate. Returns the item it
// superseded, if any.
func (h *HeldQueue) Put(item HeldItem, candidate []byte) (*HeldItem, error) {
	prev, _, err := h.Current()
	if err != nil {
		// A corrupt held slot must not wedge the queue; the new item
		// overwrites it and the caller's audit trail records the put.
		prev = nil
	}
	report, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("agent: marshal held report: %w", err)
	}
	if err := lastsigned.WriteAtomic(h.candidatePath(), candidate); err != nil {
		return nil, fmt.Errorf("agent: write held candidate: %w", err)
	}
	if err := lastsigned.WriteAtomic(h.reportPath(), report); err != nil {
		return nil, fmt.Errorf("agent: write held report: %w", err)
	}
	return prev, nil
}

// Clear removes the held item (after operator approval or refusal
// cleanup).
func (h *HeldQueue) Clear() error {
	for _, p := range []string{h.candidatePath(), h.reportPath()} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("agent: clear held: %w", err)
		}
	}
	return nil
}
