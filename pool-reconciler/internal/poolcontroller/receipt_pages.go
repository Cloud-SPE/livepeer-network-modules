package poolcontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

func (c *Client) ListAllWorkReceipts(ctx context.Context, round, status string) ([]WorkReceipt, string, error) {
	var result []WorkReceipt
	snapshot, cursor := "", ""
	seen := map[string]bool{}
	for {
		query := url.Values{"round_id": {round}, "status": {status}, "limit": {strconv.Itoa(500)}, "pagination": {"true"}, "snapshot": {snapshot}, "cursor": {cursor}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.adminURL("/admin/v1/work-receipts", query), nil)
		if err != nil {
			return nil, "", err
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		response, err := c.client.Do(req)
		if err != nil {
			return nil, "", err
		}
		var page struct {
			Receipts   []WorkReceipt `json:"receipts"`
			Snapshot   string        `json:"snapshot"`
			NextCursor string        `json:"next_cursor"`
			Total      int           `json:"total"`
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			return nil, "", fmt.Errorf("receipt page unavailable: HTTP %d", response.StatusCode)
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&page)
		response.Body.Close()
		if err != nil {
			return nil, "", err
		}
		if page.Snapshot == "" || snapshot != "" && snapshot != page.Snapshot {
			return nil, "", fmt.Errorf("receipt snapshot missing or changed")
		}
		snapshot = page.Snapshot
		for _, receipt := range page.Receipts {
			if receipt.ID == "" || seen[receipt.ID] {
				return nil, "", fmt.Errorf("duplicate or invalid receipt")
			}
			seen[receipt.ID] = true
			result = append(result, receipt)
		}
		if page.NextCursor == "" {
			if len(result) != page.Total {
				return nil, "", fmt.Errorf("receipt collection incomplete")
			}
			return result, snapshot, nil
		}
		if len(page.Receipts) == 0 || page.NextCursor <= cursor {
			return nil, "", fmt.Errorf("receipt cursor did not advance")
		}
		cursor = page.NextCursor
	}
}
