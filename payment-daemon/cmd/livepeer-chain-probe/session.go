package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ---------------------------------------------------------------------------

type httpResult struct {
	status  int
	errCode string
	body    string
	headers http.Header
	decoded map[string]any
}

func (r *httpResult) field(name string) any {
	if r.decoded == nil {
		_ = json.Unmarshal([]byte(r.body), &r.decoded)
	}
	return r.decoded[name]
}

func postJSON(url string, headers map[string]string, body string) (*httpResult, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return &httpResult{
		status:  resp.StatusCode,
		errCode: resp.Header.Get("Livepeer-Error"),
		body:    readAll(resp.Body),
		headers: resp.Header.Clone(),
	}, nil
}

// waitForOffering blocks until the broker reports this capability ready.
func waitForOffering(cfg config, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	last := "never scraped"
	for time.Now().Before(deadline) {
		resp, err := http.Get(cfg.brokerURL + "/registry/health")
		if err == nil {
			var doc struct {
				Capabilities []struct {
					ID         string `json:"id"`
					OfferingID string `json:"offering_id"`
					Status     string `json:"status"`
				} `json:"capabilities"`
			}
			body := readAll(resp.Body)
			_ = resp.Body.Close()
			if json.Unmarshal([]byte(body), &doc) == nil {
				for _, c := range doc.Capabilities {
					if c.ID == cfg.capability && c.OfferingID == cfg.offering {
						// Both are selectable per the broker's own
						// selection rules; waiting only for "ready"
						// would stall on a healthy-enough backend.
						if c.Status == "ready" || c.Status == "degraded" {
							return nil
						}
						last = c.Status
					}
				}
			}
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("offering %s/%s never became ready (last status %q) — "+
		"check that the offering's backend URL points at this probe's fake runner",
		cfg.capability, cfg.offering, last)
}
