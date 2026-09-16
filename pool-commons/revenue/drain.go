package revenue

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
	"io"
	"net/http"
	"strings"
	"time"
)

// DrainReport proves the source cannot add new obligations. Historical round
// closure is checked separately before retiring its participation.
type DrainReport struct {
	PoolID               string    `json:"pool_id"`
	SourceID             string    `json:"source_id"`
	BrokerID             string    `json:"broker_id"`
	ChainID              uint64    `json:"chain_id"`
	Payee                string    `json:"payee"`
	Draining             bool      `json:"draining"`
	Frozen               bool      `json:"frozen"`
	FrozenAt             time.Time `json:"frozen_at"`
	Complete             bool      `json:"complete"`
	IncompleteReason     string    `json:"incomplete_reason,omitempty"`
	ActiveAuthorizations uint64    `json:"active_authorizations"`
	PendingRedemptions   uint64    `json:"pending_redemptions"`
	PendingOperations    uint64    `json:"pending_operations"`
	UndeliveredReceipts  uint64    `json:"undelivered_receipts"`
	LastReceiptRound     int64     `json:"last_receipt_round"`
	LastRedemptionRound  int64     `json:"last_redemption_round"`
	CompleteThroughRound int64     `json:"complete_through_round"`
	ObservedAt           time.Time `json:"observed_at"`
}

func (r DrainReport) Validate(source Source, after int64, now time.Time) error {
	if err := source.Validate(); err != nil {
		return err
	}
	if r.PoolID != source.PoolID || r.SourceID != source.SourceID || r.BrokerID != source.BrokerID || r.ChainID != source.ChainID || !strings.EqualFold(r.Payee, source.Payee) {
		return fmt.Errorf("retirement source identity mismatch")
	}
	if !r.Draining || !r.Frozen || !r.Complete || r.ActiveAuthorizations != 0 || r.PendingRedemptions != 0 || r.PendingOperations != 0 || r.UndeliveredReceipts != 0 {
		return fmt.Errorf("source has not finished draining: %s", r.IncompleteReason)
	}
	if after < 0 || r.LastReceiptRound > after || r.LastRedemptionRound > after || r.CompleteThroughRound < after {
		return fmt.Errorf("source accounting prefix not ready for retirement")
	}
	if r.FrozenAt.IsZero() || r.FrozenAt.After(now.Add(time.Minute)) || r.ObservedAt.IsZero() || now.Sub(r.ObservedAt) > 2*time.Minute || r.ObservedAt.After(now.Add(time.Minute)) {
		return fmt.Errorf("source retirement evidence stale")
	}
	return nil
}
func CollectDrain(ctx context.Context, source Source) (DrainReport, error) {
	var out DrainReport
	client, err := serviceauth.HTTPSClientWithCAFile(source.URL, source.PoolID, source.TokenFile, source.CAFile)
	if err != nil {
		return out, err
	}
	client.Timeout = 90 * time.Second
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(source.URL, "/")+"/reporting/v1/source", nil)
	if err != nil {
		return out, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return out, fmt.Errorf("source drain proof unavailable: HTTP %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&out); err != nil {
		return out, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return out, fmt.Errorf("trailing source drain data")
	}
	return out, nil
}
