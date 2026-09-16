package portal

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberreport"
)

//go:embed web/*
var webFiles embed.FS
var pageTemplate = template.Must(template.New("index.html").Funcs(template.FuncMap{"stamp": func(t time.Time) string {
	if t.IsZero() {
		return "Not observed"
	}
	return t.UTC().Format("02 Jan 2006, 15:04 UTC")
}, "percent": func(bps uint64) string { return fmt.Sprintf("%d.%02d%%", bps/100, bps%100) }}).ParseFS(webFiles, "web/index.html"))

type pageTerms struct {
	Version            string `json:"version"`
	EffectiveRound     uint64 `json:"effective_round"`
	WindowRounds       uint64 `json:"window_rounds"`
	CommissionBPS      uint64 `json:"commission_bps"`
	ParticipationRules string `json:"participation_rules"`
}
type pageMembership struct {
	PoolID      string      `json:"pool_id"`
	Wallet      string      `json:"wallet"`
	Joined      bool        `json:"joined"`
	Terms       []pageTerms `json:"terms"`
	Acceptances []struct {
		Version string    `json:"terms_version"`
		At      time.Time `json:"accepted_at"`
	} `json:"acceptances"`
}
type pageHost struct {
	ID        string `json:"id"`
	HostLabel string `json:"host_label"`
	Status    string `json:"status"`
	Details   pageHostDetails
	Error     string
	OptOuts   []pageOptOut
}
type pageHostDetails struct {
	GPUs       []pageGPU `json:"gpus"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Contested  []struct {
		GPUUUID string `json:"gpu_uuid"`
		Detail  string `json:"detail"`
	} `json:"contested_gpus"`
	Apply *struct {
		Revision   string    `json:"revision"`
		ReportedAt time.Time `json:"reported_at"`
		Services   []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"services"`
	} `json:"apply"`
}
type pageGPU struct {
	ID         string `json:"hardware_unit_id"`
	UUID       string `json:"gpu_uuid"`
	Model      string `json:"gpu_model"`
	State      string `json:"state"`
	Placements []struct {
		TemplateID string `json:"template_id"`
		State      string `json:"state"`
		Reason     string `json:"reason_code"`
		Evidence   string `json:"evidence"`
	} `json:"placements"`
}
type pageOptOut struct {
	ID             string `json:"id"`
	TemplateID     string `json:"template_id"`
	HardwareUnitID string `json:"hardware_unit_id"`
	Reason         string `json:"reason"`
}
type pageTransfer struct {
	ID                string    `json:"id"`
	DeviceID          string    `json:"device_id"`
	DestinationPoolID string    `json:"destination_pool_id"`
	Phase             string    `json:"phase"`
	LastError         string    `json:"last_error"`
	UpdatedAt         time.Time `json:"updated_at"`
}
type pageRegion struct {
	Public      publicRegion
	PublicError string
	Region      Region
	Membership  pageMembership
	Terms       *pageTerms
	Accepted    bool
	Error       string
	Hosts       []pageHost
	Transfers   []pageTransfer
	Report      memberreport.RegionalResult
}
type pageData struct {
	Wallet      string
	Regions     []pageRegion
	Aggregates  []memberreport.Aggregate
	ReportError string
}

func (c *RegionalClient) readJSON(ctx context.Context, session Session, path string, dst any) error {
	response, err := c.Do(ctx, session, "GET", path, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("regional request returned %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if err != nil || len(raw) > 4<<20 {
		return fmt.Errorf("regional response incomplete")
	}
	return json.Unmarshal(raw, dst)
}
func (s *Server) registerPage(mux *http.ServeMux) {
	mux.HandleFunc("GET /assets/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name != "portal.css" && name != "portal.js" {
			http.NotFound(w, r)
			return
		}
		raw, err := webFiles.ReadFile("web/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if name == "portal.css" {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		}
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("GET /{$}", s.page)
}
func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	data := pageData{}
	session, err := s.session(r)
	if err == nil {
		data.Wallet = session.Wallet
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	results := make(chan pageRegion, len(s.Regions))
	var reports []memberreport.RegionalResult
	var wg sync.WaitGroup
	if data.Wallet != "" {
		wg.Add(1)
		go func() { defer wg.Done(); reports = s.collectReports(ctx, session) }()
	}
	for _, client := range s.Regions {
		go func(client *RegionalClient) {
			region := pageRegion{Region: client.Region}
			public, publicErr := client.Public(ctx)
			region.Public = public
			if publicErr != nil {
				region.PublicError = "Regional public status unavailable"
			}
			if data.Wallet == "" {
				results <- region
				return
			}
			if err := client.readJSON(ctx, session, "/member/v1/membership", &region.Membership); err != nil || region.Membership.PoolID != client.Region.PoolID || region.Membership.Wallet != session.Wallet {
				region.Error = "Regional membership unavailable. No change has been made."
				results <- region
				return
			}
			if n := len(region.Membership.Terms); n > 0 {
				region.Terms = &region.Membership.Terms[n-1]
				for _, a := range region.Membership.Acceptances {
					if a.Version == region.Terms.Version {
						region.Accepted = true
					}
				}
			}
			if region.Membership.Joined {
				var hosts struct {
					Enrollments []pageHost `json:"enrollments"`
				}
				if err := client.readJSON(ctx, session, "/member/v1/enrollments", &hosts); err != nil {
					region.Error = "Regional host status unavailable."
				} else {
					region.Hosts = hosts.Enrollments
				}
				// A bounded set of workers avoids turning a large membership into unbounded HTTP fanout.
				jobs := make(chan int)
				var workers sync.WaitGroup
				for range 4 {
					workers.Add(1)
					go func() {
						defer workers.Done()
						for i := range jobs {
							host := &region.Hosts[i]
							if !safeID.MatchString(host.ID) {
								host.Error = "Invalid regional host identity"
								continue
							}
							base := "/member/v1/enrollments/" + host.ID
							if err := client.readJSON(ctx, session, base+"/status", &host.Details); err != nil {
								host.Error = "Host details unavailable"
							}
							var optouts struct {
								OptOuts []pageOptOut `json:"opt_outs"`
							}
							if err := client.readJSON(ctx, session, base+"/opt-outs", &optouts); err != nil {
								host.Error = "Host settings unavailable"
							} else {
								host.OptOuts = optouts.OptOuts
							}
						}
					}()
				}
				for i := range region.Hosts {
					jobs <- i
				}
				close(jobs)
				workers.Wait()
				var transfers struct {
					Transfers []pageTransfer `json:"transfers"`
				}
				if err := client.readJSON(ctx, session, "/member/v1/device-transfers", &transfers); err != nil {
					region.Error = "Regional transfer status unavailable"
				} else {
					region.Transfers = transfers.Transfers
				}
			}
			results <- region
		}(client)
	}
	for range s.Regions {
		data.Regions = append(data.Regions, <-results)
	}
	wg.Wait()
	sort.Slice(data.Regions, func(i, j int) bool { return data.Regions[i].Region.Name < data.Regions[j].Region.Name })
	for i := range data.Regions {
		for _, report := range reports {
			if report.PoolID == data.Regions[i].Region.PoolID {
				data.Regions[i].Report = report
			}
		}
	}
	if data.Wallet != "" {
		data.Aggregates, err = memberreport.Combine(reports)
		if err != nil {
			data.ReportError = "Compatible aggregate unavailable"
		}
	}
	var rendered bytes.Buffer
	if err := pageTemplate.Execute(&rendered, data); err != nil {
		http.Error(w, "Member page unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(rendered.Bytes())
}
