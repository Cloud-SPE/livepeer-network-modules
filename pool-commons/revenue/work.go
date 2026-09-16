package revenue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Contribution is the source-qualified monetary identity committed by both
// broker and controller. Native work units never substitute for billed value.
type Contribution struct {
	TermsVersion string `json:"terms_version,omitempty"`
	ID           string `json:"id"`
	PoolID       string `json:"pool_id"`
	SourceID     string `json:"source_id"`
	RoundID      string `json:"round_id"`
	Member       string `json:"member"`
	Offering     string `json:"offering"`
	Backend      string `json:"backend"`
	AmountWei    string `json:"amount_wei"`
}

func WorkDigest(entries []Contribution) (string, error) {
	items := append([]Contribution{}, entries...)
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	for i, item := range items {
		if item.ID == "" || i > 0 && item.ID == items[i-1].ID {
			return "", fmt.Errorf("duplicate or missing contribution identity")
		}
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

type WorkReport struct {
	PoolID             string    `json:"pool_id"`
	SourceID           string    `json:"source_id"`
	BrokerID           string    `json:"broker_id"`
	Round              int64     `json:"round"`
	Complete           bool      `json:"complete"`
	IncompleteReason   string    `json:"incomplete_reason,omitempty"`
	ClosedThroughRound int64     `json:"closed_through_round"`
	ReceiptCount       uint64    `json:"receipt_count"`
	ReceiptDigest      string    `json:"receipt_digest"`
	ObservedAt         time.Time `json:"observed_at"`
}

func (r WorkReport) Validate(source Source, round int64, now time.Time) error {
	if source.PoolID == "" || source.SourceID == "" || r.PoolID != source.PoolID || r.SourceID != source.SourceID || r.BrokerID != source.BrokerID || r.Round != round {
		return fmt.Errorf("work report source identity mismatch")
	}
	if !r.Complete || r.ClosedThroughRound < round {
		return fmt.Errorf("work source incomplete: %s", r.IncompleteReason)
	}
	if r.ObservedAt.IsZero() || now.Sub(r.ObservedAt) > 2*time.Minute || r.ObservedAt.After(now.Add(time.Minute)) || !hexSize(r.ReceiptDigest, 32, false) {
		return fmt.Errorf("work report stale or invalid")
	}
	return nil
}

func CollectWork(ctx context.Context, source Source, round int64) (WorkReport, error) {
	var result WorkReport
	client, err := serviceauth.HTTPSClientWithCAFile(source.URL, source.PoolID, source.TokenFile, source.CAFile)
	if err != nil {
		return result, err
	}
	client.Timeout = 30 * time.Second
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(source.URL, "/")+"/reporting/v1/work/"+strconv.FormatInt(round, 10), nil)
	if err != nil {
		return result, err
	}
	response, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return result, fmt.Errorf("work source %s returned HTTP %d", source.SourceID, response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return result, fmt.Errorf("invalid trailing work report")
	}
	return result, result.Validate(source, round, time.Now())
}
