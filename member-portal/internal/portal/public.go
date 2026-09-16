package portal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type publicRegion struct {
	PoolID           string    `json:"pool_id"`
	ObservedAt       time.Time `json:"observed_at"`
	ControllerStatus string    `json:"controller_status"`
	OfferingStatus   string    `json:"offering_status"`
	Offerings        []struct {
		TemplateID  string `json:"template_id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Capability  string `json:"capability"`
		OfferingID  string `json:"offering_id"`
	} `json:"offerings"`
}

func (c *RegionalClient) Public(ctx context.Context) (publicRegion, error) {
	var out publicRegion
	target := *c.origin
	target.Path = "/member/v1/public-region"
	target.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
	if err != nil {
		return out, err
	}
	response, err := c.http.Do(req)
	if err != nil {
		return out, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return out, fmt.Errorf("regional public status unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return out, fmt.Errorf("regional public status incomplete")
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	if out.PoolID != c.Region.PoolID || out.ObservedAt.IsZero() {
		return out, fmt.Errorf("regional public identity mismatch")
	}
	return out, nil
}
