package livehealthfetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

type Fetcher interface {
	Fetch(ctx context.Context, workerURL string) (*types.RouteHealthSnapshot, error)
}

type HTTPFetcher struct {
	client *http.Client
}

func New(timeout time.Duration) *HTTPFetcher {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &HTTPFetcher{
		client: &http.Client{Timeout: timeout},
	}
}

func (f *HTTPFetcher) Fetch(ctx context.Context, workerURL string) (*types.RouteHealthSnapshot, error) {
	target := strings.TrimRight(workerURL, "/") + "/registry/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("livehealthfetcher: HTTP %d", resp.StatusCode)
	}
	var out types.RouteHealthSnapshot
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}
