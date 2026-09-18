package portal

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberreport"
)

type reportCache struct {
	mu      sync.Mutex
	entries map[string]memberreport.RegionalResult
}

func (c *reportCache) get(key string) (memberreport.RegionalResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result, ok := c.entries[key]
	if ok && time.Since(result.FetchedAt) > 24*time.Hour {
		delete(c.entries, key)
		ok = false
	}
	return result, ok
}
func (c *reportCache) put(key string, result memberreport.RegionalResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]memberreport.RegionalResult{}
	}
	if len(c.entries) >= 1024 {
		for key := range c.entries {
			delete(c.entries, key)
			break
		}
	}
	c.entries[key] = result
}
func (s *Server) reports(w http.ResponseWriter, r *http.Request) {
	session, err := s.session(r)
	if err != nil {
		http.Error(w, "sign in required", 401)
		return
	}
	regions := s.collectReports(r.Context(), session)
	aggregate, err := memberreport.Combine(regions)
	if err != nil {
		http.Error(w, "qualified aggregate unavailable", 503)
		return
	}
	respond(w, 200, map[string]any{"wallet": session.Wallet, "observed_at": time.Now().UTC(), "regions": regions, "aggregate_windows": aggregate})
}

func (s *Server) collectReports(ctx context.Context, session Session) []memberreport.RegionalResult {
	ch := make(chan memberreport.RegionalResult, len(s.Regions))
	for _, client := range s.Regions {
		go func(client *RegionalClient) {
			result := memberreport.RegionalResult{PoolID: client.Region.PoolID, Name: client.Region.Name, Status: "unavailable"}
			key := session.Wallet + "/" + client.Region.PoolID
			response, err := client.Do(ctx, session, "GET", "/member/v1/regional-report", nil)
			if err == nil {
				defer response.Body.Close()
				if response.StatusCode == 200 {
					raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
					var report memberreport.Report
					if err == nil && len(raw) <= 4<<20 && json.Unmarshal(raw, &report) == nil && memberreport.Validate(report, client.Region.PoolID, session.Wallet) == nil {
						result.Status = "fresh"
						result.FetchedAt = time.Now().UTC()
						result.Report = &report
						s.cache.put(key, result)
						ch <- result
						return
					}
				}
			}
			if cached, ok := s.cache.get(key); ok {
				result = cached
				result.Status = "stale"
			}
			ch <- result
		}(client)
	}
	regions := make([]memberreport.RegionalResult, 0, len(s.Regions))
	for range s.Regions {
		regions = append(regions, <-ch)
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i].Name < regions[j].Name })

	return regions
}
