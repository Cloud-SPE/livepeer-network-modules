package poolcontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

func (c *Client) RevenueSources(ctx context.Context, pool string, round int64) ([]revenue.Source, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.adminURL("/admin/v1/revenue-sources", url.Values{"round": {strconv.FormatInt(round, 10)}}), nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("source registry unavailable: HTTP %d", response.StatusCode)
	}
	var payload struct {
		PoolID  string `json:"pool_id"`
		Sources []struct {
			Source revenue.Source `json:"source"`
		} `json:"sources"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.PoolID != pool {
		return nil, fmt.Errorf("source registry pool mismatch")
	}
	result := []revenue.Source{}
	seen := map[string]bool{}
	for _, row := range payload.Sources {
		if row.Source.PoolID != pool || row.Source.SourceID == "" || seen[row.Source.SourceID] {
			return nil, fmt.Errorf("source registry contains duplicate or wrong-pool source")
		}
		seen[row.Source.SourceID] = true
		result = append(result, row.Source)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no registered revenue sources for round %d", round)
	}
	return result, nil
}

// RevenueStartRound uses the complete registry, including retired sources. A
// round-specific empty source set is not evidence that history can be skipped.
func (c *Client) RevenueStartRound(ctx context.Context, pool string) (uint64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.adminURL("/admin/v1/revenue-sources", nil), nil)
	if err != nil {
		return 0, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("source registry unavailable: HTTP %d", res.StatusCode)
	}
	var payload struct {
		PoolID  string `json:"pool_id"`
		Sources []struct {
			Source     revenue.Source `json:"source"`
			StartRound *int64         `json:"start_round"`
		} `json:"sources"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&payload); err != nil {
		return 0, err
	}
	if payload.PoolID != pool || len(payload.Sources) == 0 {
		return 0, fmt.Errorf("source registry missing or wrong-pool")
	}
	earliest := ^uint64(0)
	seen := map[string]bool{}
	for _, row := range payload.Sources {
		if row.Source.PoolID != pool || row.Source.SourceID == "" || seen[row.Source.SourceID] || row.StartRound == nil || *row.StartRound < 0 {
			return 0, fmt.Errorf("invalid source participation boundary")
		}
		seen[row.Source.SourceID] = true
		if uint64(*row.StartRound) < earliest {
			earliest = uint64(*row.StartRound)
		}
	}
	return earliest, nil
}
