package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/configgen"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageError(stderr)
	}

	switch args[0] {
	case "generate-broker-config":
		return runGenerateBrokerConfig(args[1:], stdout)
	case "serve":
		return runServe(args[1:], stdout, stderr)
	case "version":
		_, err := fmt.Fprintln(stdout, version)
		return err
	default:
		return usageError(stderr)
	}
}

func runGenerateBrokerConfig(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("generate-broker-config", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	configPath := fs.String("config", "", "path to pool-controller config")
	outputPath := fs.String("output", "", "optional output path; stdout when empty")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return errors.New("--config is required")
	}

	cfg, err := config.LoadFile(*configPath)
	if err != nil {
		return err
	}

	rendered, err := configgen.GenerateYAML(cfg)
	if err != nil {
		return err
	}

	if *outputPath == "" {
		_, err = stdout.Write(rendered)
		return err
	}

	return os.WriteFile(*outputPath, rendered, 0o644)
}

func runServe(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	configPath := fs.String("config", "", "path to pool-controller config")
	dataDir := fs.String("data-dir", "", "pool-controller data directory")
	listenAddr := fs.String("listen", ":8080", "HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return errors.New("--config is required")
	}
	if *dataDir == "" {
		return errors.New("--data-dir is required")
	}

	cfg, err := config.LoadFile(*configPath)
	if err != nil {
		return err
	}
	adminToken, err := resolveAdminToken(cfg)
	if err != nil {
		return err
	}
	rendered, err := configgen.GenerateYAML(cfg)
	if err != nil {
		return err
	}
	stateRepo, err := repo.Open(*dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = stateRepo.Close() }()
	state := &runtimeState{configPath: *configPath, repo: stateRepo, adminToken: adminToken}
	if err := state.Replace(cfg, rendered, "startup"); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:    *listenAddr,
		Handler: newServeMux(state),
	}
	_, _ = fmt.Fprintf(stdout, "listening on %s\n", *listenAddr)
	return srv.ListenAndServe()
}

type runtimeState struct {
	mu         sync.RWMutex
	configPath string
	repo       *repo.StateRepo
	adminToken string
	cfg        *config.Config
	rendered   []byte
	latest     *repo.Snapshot
}

func (s *runtimeState) Replace(cfg *config.Config, rendered []byte, source string) error {
	var latest *repo.Snapshot
	if s.repo != nil {
		configRaw, err := os.ReadFile(s.configPath)
		if err != nil {
			return fmt.Errorf("read active config for snapshot: %w", err)
		}
		snap := repo.Snapshot{
			ID:                 time.Now().UTC().Format(time.RFC3339Nano),
			CreatedAt:          time.Now().UTC(),
			Source:             source,
			MemberCount:        len(cfg.Members),
			RenderedBytes:      len(rendered),
			ConfigYAML:         string(configRaw),
			RenderedBrokerYAML: string(rendered),
		}
		if err := s.repo.SaveSnapshot(snap); err != nil {
			return err
		}
		latest = &snap
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
	s.rendered = append([]byte(nil), rendered...)
	s.latest = latest
	return nil
}

func (s *runtimeState) Snapshot() (*config.Config, []byte, *repo.Snapshot) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest *repo.Snapshot
	if s.latest != nil {
		copySnap := *s.latest
		latest = &copySnap
	}
	return s.cfg, append([]byte(nil), s.rendered...), latest
}

func (s *runtimeState) Reload() error {
	cfg, err := config.LoadFile(s.configPath)
	if err != nil {
		return err
	}
	rendered, err := configgen.GenerateYAML(cfg)
	if err != nil {
		return err
	}
	token, err := resolveAdminToken(cfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.adminToken = token
	s.mu.Unlock()
	return s.Replace(cfg, rendered, "reload")
}

func newServeMux(state *runtimeState) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ready\n")
	})
	mux.HandleFunc("GET /public/v1/summary", func(w http.ResponseWriter, _ *http.Request) {
		cfg, _, _ := state.Snapshot()
		rounds, err := state.repo.ListRoundReceipts(1)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		backendCount := 0
		if cfg != nil {
			for _, member := range cfg.Members {
				backendCount += len(member.Backends)
			}
		}
		latestRoundID := ""
		if len(rounds) > 0 {
			latestRoundID = rounds[0].RoundID
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			MemberCount       int    `json:"member_count"`
			BackendCount      int    `json:"backend_count"`
			OfferingCount     int    `json:"offering_count"`
			LatestClosedRound string `json:"latest_closed_round,omitempty"`
		}{
			MemberCount:       len(cfg.Members),
			BackendCount:      backendCount,
			OfferingCount:     len(buildOfferingViews(cfg)),
			LatestClosedRound: latestRoundID,
		})
	})
	mux.HandleFunc("GET /public/v1/rounds", func(w http.ResponseWriter, r *http.Request) {
		limit, err := parsePositiveIntQuery(r, "limit", 10)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		items, err := state.repo.ListRoundReceipts(limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Rounds []publicRoundView `json:"rounds"`
		}{Rounds: buildPublicRoundViews(items)})
	})
	mux.HandleFunc("GET /public/v1/offerings", func(w http.ResponseWriter, _ *http.Request) {
		cfg, _, _ := state.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Offerings []offeringView `json:"offerings"`
		}{Offerings: buildOfferingViews(cfg)})
	})
	mux.HandleFunc("GET /public/v1/member-payouts", func(w http.ResponseWriter, r *http.Request) {
		memberEthAddress := strings.TrimSpace(r.URL.Query().Get("member_eth_address"))
		if memberEthAddress == "" {
			http.Error(w, "member_eth_address is required", http.StatusBadRequest)
			return
		}
		limit, err := parsePositiveIntQuery(r, "limit", 100)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		items, err := state.repo.ListPayoutIntents(0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items = filterPayoutIntents(items, r.URL.Query().Get("round_id"), memberEthAddress, "")
		summary := buildMemberPayoutSummaryViews(items)
		if len(summary) == 0 {
			summary = []memberPayoutSummaryView{{
				MemberEthAddress: memberEthAddress,
				IntentCount:      0,
				PendingWei:       "0",
				ExportedWei:      "0",
				SubmittedWei:     "0",
				PaidWei:          "0",
				FailedWei:        "0",
			}}
		}
		if limit > 0 && len(items) > limit {
			items = items[:limit]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			MemberEthAddress string                  `json:"member_eth_address"`
			Summary          memberPayoutSummaryView `json:"summary"`
			Intents          []payoutIntentView      `json:"intents"`
		}{
			MemberEthAddress: memberEthAddress,
			Summary:          summary[0],
			Intents:          buildPayoutIntentViews(items),
		})
	})
	mux.HandleFunc("GET /admin/v1/broker-config", withAdminAuth(state, func(w http.ResponseWriter, _ *http.Request) {
		_, rendered, _ := state.Snapshot()
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(rendered)
	}))
	mux.HandleFunc("GET /admin/v1/members", withAdminAuth(state, func(w http.ResponseWriter, _ *http.Request) {
		cfg, _, _ := state.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Members []memberView `json:"members"`
		}{Members: buildMemberViews(cfg)})
	}))
	mux.HandleFunc("GET /admin/v1/offerings", withAdminAuth(state, func(w http.ResponseWriter, _ *http.Request) {
		cfg, _, _ := state.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Offerings []offeringView `json:"offerings"`
		}{Offerings: buildOfferingViews(cfg)})
	}))
	mux.HandleFunc("GET /admin/v1/state", withAdminAuth(state, func(w http.ResponseWriter, _ *http.Request) {
		cfg, rendered, latest := state.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			MemberCount   int              `json:"member_count"`
			RenderedBytes int              `json:"rendered_bytes"`
			Latest        *snapshotSummary `json:"latest_snapshot,omitempty"`
		}{
			MemberCount:   len(cfg.Members),
			RenderedBytes: len(rendered),
			Latest:        summarizeSnapshot(latest),
		})
	}))
	mux.HandleFunc("GET /admin/v1/snapshots", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		items, err := state.repo.ListSnapshots(20)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Snapshots []snapshotSummary `json:"snapshots"`
		}{Snapshots: summarizeSnapshots(items)})
	}))
	mux.HandleFunc("GET /admin/v1/work-receipts", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		limit, err := parsePositiveIntQuery(r, "limit", 50)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		items, err := state.repo.ListWorkReceipts(limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if roundID := strings.TrimSpace(r.URL.Query().Get("round_id")); roundID != "" {
			filtered := make([]types.WorkReceipt, 0, len(items))
			for _, item := range items {
				if item.RoundID == roundID {
					filtered = append(filtered, item)
				}
			}
			items = filtered
		}
		statusFilter := r.URL.Query().Get("status")
		if statusFilter != "" {
			filtered := make([]types.WorkReceipt, 0, len(items))
			for _, item := range items {
				if item.Status == statusFilter {
					filtered = append(filtered, item)
				}
			}
			items = filtered
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Receipts []workReceiptView `json:"receipts"`
		}{Receipts: buildWorkReceiptViews(items)})
	}))
	mux.HandleFunc("POST /admin/v1/work-receipts", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		var receipt types.WorkReceipt
		if err := json.NewDecoder(r.Body).Decode(&receipt); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := validateWorkReceipt(receipt); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := state.repo.SaveWorkReceipt(receipt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items, err := state.repo.ListWorkReceipts(1)
		if err != nil || len(items) == 0 {
			http.Error(w, "receipt persisted but could not be reloaded", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Status  string          `json:"status"`
			Receipt workReceiptView `json:"receipt"`
		}{
			Status:  "upserted",
			Receipt: buildWorkReceiptViews(items)[0],
		})
	}))
	mux.HandleFunc("GET /admin/v1/round-receipts", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		items, err := state.repo.ListRoundReceipts(20)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Rounds []roundReceiptView `json:"rounds"`
		}{Rounds: buildRoundReceiptViews(items)})
	}))
	mux.HandleFunc("GET /admin/v1/payout-intents", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		limit, err := parsePositiveIntQuery(r, "limit", 100)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		items, err := state.repo.ListPayoutIntents(limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items, err = normalizeExpiredPayoutIntentLeases(state.repo, items, time.Now().UTC())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items = filterPayoutIntents(items, r.URL.Query().Get("round_id"), r.URL.Query().Get("member_eth_address"), r.URL.Query().Get("status"))
		format := strings.TrimSpace(r.URL.Query().Get("format"))
		if format == "csv" {
			writePayoutIntentCSV(w, items)
			return
		}
		if format != "" && format != "json" {
			http.Error(w, `format must be "json" or "csv"`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Intents []payoutIntentView `json:"intents"`
		}{Intents: buildPayoutIntentViews(items)})
	}))
	mux.HandleFunc("GET /admin/v1/member-payouts", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		items, err := state.repo.ListPayoutIntents(0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items, err = normalizeExpiredPayoutIntentLeases(state.repo, items, time.Now().UTC())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items = filterPayoutIntents(items, r.URL.Query().Get("round_id"), r.URL.Query().Get("member_eth_address"), "")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Members []memberPayoutSummaryView `json:"members"`
		}{Members: buildMemberPayoutSummaryViews(items)})
	}))
	mux.HandleFunc("GET /admin/v1/payout-rounds", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		items, err := state.repo.ListPayoutIntents(0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items, err = normalizeExpiredPayoutIntentLeases(state.repo, items, time.Now().UTC())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items = filterPayoutIntents(items, r.URL.Query().Get("round_id"), r.URL.Query().Get("member_eth_address"), "")
		limit, err := parsePositiveIntQuery(r, "limit", 100)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		summaries := buildRoundPayoutSummaryViews(items)
		if limit > 0 && len(summaries) > limit {
			summaries = summaries[:limit]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Rounds []roundPayoutSummaryView `json:"rounds"`
		}{Rounds: summaries})
	}))
	mux.HandleFunc("GET /admin/v1/payout-alerts", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		limit, err := parsePositiveIntQuery(r, "limit", 100)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		submittedOlderThanSeconds, err := parsePositiveIntQuery(r, "submitted_older_than_seconds", 900)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		failedOlderThanSeconds, err := parsePositiveIntQuery(r, "failed_older_than_seconds", 3600)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		leaseExpiresWithinSeconds, err := parsePositiveIntQuery(r, "lease_expires_within_seconds", 60)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		retryCountAtLeast, err := parsePositiveIntQuery(r, "retry_count_at_least", 3)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		recentRequeueWithinSeconds, err := parsePositiveIntQuery(r, "recent_requeue_within_seconds", 900)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		items, err := state.repo.ListPayoutIntents(0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		now := time.Now().UTC()
		items, err = normalizeExpiredPayoutIntentLeases(state.repo, items, now)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items = filterPayoutIntents(items, r.URL.Query().Get("round_id"), r.URL.Query().Get("member_eth_address"), r.URL.Query().Get("status"))
		alerts := buildPayoutAlertViews(items, now, payoutAlertThresholds{
			SubmittedOlderThan:  time.Duration(submittedOlderThanSeconds) * time.Second,
			FailedOlderThan:     time.Duration(failedOlderThanSeconds) * time.Second,
			LeaseExpiresWithin:  time.Duration(leaseExpiresWithinSeconds) * time.Second,
			RetryCountAtLeast:   uint64(retryCountAtLeast),
			RecentRequeueWithin: time.Duration(recentRequeueWithinSeconds) * time.Second,
		})
		if limit > 0 && len(alerts) > limit {
			alerts = alerts[:limit]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Summary payoutAlertSummaryView `json:"summary"`
			Alerts  []payoutAlertView      `json:"alerts"`
		}{
			Summary: summarizePayoutAlerts(alerts),
			Alerts:  alerts,
		})
	}))
	mux.HandleFunc("POST /admin/v1/round-receipts", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		var receipt types.RoundReceipt
		if err := json.NewDecoder(r.Body).Decode(&receipt); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := validateRoundReceipt(receipt); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := state.repo.SaveRoundReceipt(receipt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items, err := state.repo.ListRoundReceipts(1)
		if err != nil || len(items) == 0 {
			http.Error(w, "round receipt persisted but could not be reloaded", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Status string           `json:"status"`
			Round  roundReceiptView `json:"round"`
		}{
			Status: "upserted",
			Round:  buildRoundReceiptViews(items)[0],
		})
	}))
	mux.HandleFunc("POST /admin/v1/payout-intents/derive", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		var req payoutIntentDeriveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := validatePayoutIntentDeriveRequest(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		roundReceipt, err := loadRoundReceiptForPayouts(state.repo, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		intents := buildPayoutIntents(roundReceipt)
		for _, intent := range intents {
			if err := state.repo.SavePayoutIntent(intent); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Status  string             `json:"status"`
			Intents []payoutIntentView `json:"intents"`
		}{
			Status:  "derived",
			Intents: buildPayoutIntentViews(intents),
		})
	}))
	mux.HandleFunc("POST /admin/v1/payout-intents/export", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		var req payoutIntentExportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Format == "" {
			req.Format = "json"
		}
		if req.Format != "json" && req.Format != "csv" {
			http.Error(w, `format must be "json" or "csv"`, http.StatusBadRequest)
			return
		}
		items, err := state.repo.ListPayoutIntents(0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items, err = normalizeExpiredPayoutIntentLeases(state.repo, items, time.Now().UTC())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items = filterPayoutIntents(items, req.RoundID, req.MemberEthAddress, req.Status)
		if len(items) == 0 {
			http.Error(w, "no payout intents matched export filter", http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		for i := range items {
			if items[i].Status == "pending" {
				items[i].Status = "exported"
				items[i].ExportedAt = now
				items[i].LeasedAt = time.Time{}
				items[i].LeaseID = ""
				items[i].LeaseOwner = ""
				items[i].LeaseExpiresAt = time.Time{}
				if err := state.repo.SavePayoutIntent(items[i]); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
		}
		if req.Format == "csv" {
			writePayoutIntentCSV(w, items)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Status  string             `json:"status"`
			Intents []payoutIntentView `json:"intents"`
		}{
			Status:  "exported",
			Intents: buildPayoutIntentViews(items),
		})
	}))
	mux.HandleFunc("POST /admin/v1/payout-intents/claim", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		var req payoutIntentClaimRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := validatePayoutIntentClaimRequest(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		items, err := state.repo.ListPayoutIntents(0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items, err = normalizeExpiredPayoutIntentLeases(state.repo, items, now)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items = filterPayoutIntents(items, req.RoundID, req.MemberEthAddress, "exported")
		limit := req.Limit
		if limit <= 0 {
			limit = 25
		}
		if limit > len(items) {
			limit = len(items)
		}
		leaseID := fmt.Sprintf("%s-%d", strings.TrimSpace(req.ExecutorID), now.UnixNano())
		ttl := time.Duration(req.LeaseTTLSeconds) * time.Second
		claimed := make([]types.PayoutIntent, 0, limit)
		for i := 0; i < limit; i++ {
			items[i].Status = "leased"
			items[i].LeasedAt = now
			items[i].LeaseID = leaseID
			items[i].LeaseOwner = strings.TrimSpace(req.ExecutorID)
			items[i].LeaseExpiresAt = now.Add(ttl)
			if err := state.repo.SavePayoutIntent(items[i]); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			claimed = append(claimed, items[i])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Status  string             `json:"status"`
			LeaseID string             `json:"lease_id"`
			Intents []payoutIntentView `json:"intents"`
		}{
			Status:  "claimed",
			LeaseID: leaseID,
			Intents: buildPayoutIntentViews(claimed),
		})
	}))
	mux.HandleFunc("POST /admin/v1/payout-intents/renew", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		var req payoutIntentRenewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := validatePayoutIntentRenewRequest(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		items, err := state.repo.ListPayoutIntents(0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items, err = normalizeExpiredPayoutIntentLeases(state.repo, items, now)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		renewed := make([]types.PayoutIntent, 0)
		for _, item := range items {
			if item.Status != "leased" || item.LeaseID != strings.TrimSpace(req.LeaseID) || item.LeaseOwner != strings.TrimSpace(req.ExecutorID) {
				continue
			}
			item.LeaseExpiresAt = now.Add(time.Duration(req.LeaseTTLSeconds) * time.Second)
			if err := state.repo.SavePayoutIntent(item); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			renewed = append(renewed, item)
		}
		if len(renewed) == 0 {
			http.Error(w, "no leased payout intents matched renewal request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Status  string             `json:"status"`
			LeaseID string             `json:"lease_id"`
			Intents []payoutIntentView `json:"intents"`
		}{
			Status:  "renewed",
			LeaseID: strings.TrimSpace(req.LeaseID),
			Intents: buildPayoutIntentViews(renewed),
		})
	}))
	mux.HandleFunc("POST /admin/v1/payout-intents/release", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		var req payoutIntentReleaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := validatePayoutIntentReleaseRequest(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		items, err := state.repo.ListPayoutIntents(0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items, err = normalizeExpiredPayoutIntentLeases(state.repo, items, now)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		released := make([]types.PayoutIntent, 0)
		releaseIDs := make(map[string]struct{}, len(req.IDs))
		for _, id := range req.IDs {
			id = strings.TrimSpace(id)
			if id != "" {
				releaseIDs[id] = struct{}{}
			}
		}
		for _, item := range items {
			if item.Status != "leased" || item.LeaseID != strings.TrimSpace(req.LeaseID) || item.LeaseOwner != strings.TrimSpace(req.ExecutorID) {
				continue
			}
			if len(releaseIDs) > 0 {
				if _, ok := releaseIDs[item.ID]; !ok {
					continue
				}
			}
			item.Status = "exported"
			item.LeasedAt = time.Time{}
			item.LeaseID = ""
			item.LeaseOwner = ""
			item.LeaseExpiresAt = time.Time{}
			if err := state.repo.SavePayoutIntent(item); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			released = append(released, item)
		}
		if len(released) == 0 {
			http.Error(w, "no leased payout intents matched release request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Status  string             `json:"status"`
			LeaseID string             `json:"lease_id"`
			Intents []payoutIntentView `json:"intents"`
		}{
			Status:  "released",
			LeaseID: strings.TrimSpace(req.LeaseID),
			Intents: buildPayoutIntentViews(released),
		})
	}))
	mux.HandleFunc("POST /admin/v1/payout-intents/requeue", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		var req payoutIntentRequeueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := validatePayoutIntentRequeueRequest(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		requeued := make([]types.PayoutIntent, 0, len(req.IDs))
		for _, id := range req.IDs {
			intent, err := state.repo.GetPayoutIntent(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			intent, changed := releaseExpiredLease(intent, now)
			if changed {
				if err := state.repo.SavePayoutIntent(intent); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
			next, err := requeuePayoutIntent(intent, now)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := state.repo.SavePayoutIntent(next); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			requeued = append(requeued, next)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Status  string             `json:"status"`
			Intents []payoutIntentView `json:"intents"`
		}{
			Status:  "requeued",
			Intents: buildPayoutIntentViews(requeued),
		})
	}))
	mux.HandleFunc("POST /admin/v1/payout-intents/status", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		var req payoutIntentStatusUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := validatePayoutIntentStatusUpdateRequest(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		updated := make([]types.PayoutIntent, 0, len(req.IDs))
		for _, id := range req.IDs {
			intent, err := state.repo.GetPayoutIntent(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			intent, changed := releaseExpiredLease(intent, now)
			if changed {
				if err := state.repo.SavePayoutIntent(intent); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
			next, err := applyPayoutIntentStatus(
				intent,
				req.Status,
				strings.TrimSpace(req.LeaseID),
				strings.TrimSpace(req.ExternalRef),
				strings.TrimSpace(req.TxHash),
				strings.TrimSpace(req.FailureReason),
				now,
			)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := state.repo.SavePayoutIntent(next); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			updated = append(updated, next)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Status  string             `json:"status"`
			Intents []payoutIntentView `json:"intents"`
		}{
			Status:  "updated",
			Intents: buildPayoutIntentViews(updated),
		})
	}))
	mux.HandleFunc("POST /admin/v1/round-close", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		var req roundCloseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := validateRoundCloseRequest(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		workReceipts, err := state.repo.GetWorkReceipts(req.IncludedWorkReceiptIDs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		roundReceipt, err := buildRoundReceiptFromCloseRequest(req, workReceipts)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := state.repo.SaveRoundReceipt(roundReceipt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Status string           `json:"status"`
			Round  roundReceiptView `json:"round"`
		}{
			Status: "closed",
			Round:  buildRoundReceiptViews([]types.RoundReceipt{roundReceipt})[0],
		})
	}))
	mux.HandleFunc("POST /admin/v1/reload", withAdminAuth(state, func(w http.ResponseWriter, _ *http.Request) {
		if err := state.Reload(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cfg, rendered, latest := state.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			MemberCount   int    `json:"member_count"`
			RenderedBytes int    `json:"rendered_bytes"`
			Status        string `json:"status"`
			SnapshotID    string `json:"snapshot_id,omitempty"`
		}{
			MemberCount:   len(cfg.Members),
			RenderedBytes: len(rendered),
			Status:        "reloaded",
			SnapshotID:    latest.ID,
		})
	}))
	return mux
}

func withAdminAuth(state *runtimeState, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state.mu.RLock()
		token := state.adminToken
		state.mu.RUnlock()
		if token == "" {
			next(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func resolveAdminToken(cfg *config.Config) (string, error) {
	if token := strings.TrimSpace(cfg.AdminAuth.BearerToken); token != "" {
		return token, nil
	}
	ref := cfg.AdminAuth.BearerTokenRef
	if ref == "" {
		return "", nil
	}
	key := strings.TrimPrefix(ref, "env://")
	if key == "" {
		return "", errors.New("admin_auth.bearer_token_ref must not be empty")
	}
	value, ok := os.LookupEnv(key)
	if !ok {
		return "", fmt.Errorf("admin auth env var %q is not set", key)
	}
	if value == "" {
		return "", fmt.Errorf("admin auth env var %q is empty", key)
	}
	return value, nil
}

func parsePositiveIntQuery(r *http.Request, key string, defaultValue int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

type memberView struct {
	EthAddress  string              `json:"eth_address"`
	DisplayName string              `json:"display_name,omitempty"`
	PayoutMode  string              `json:"payout_mode,omitempty"`
	Backends    []memberBackendView `json:"backends"`
}

type memberBackendView struct {
	ID        string               `json:"id"`
	Transport string               `json:"transport"`
	URL       string               `json:"url,omitempty"`
	Auth      memberAuthView       `json:"auth,omitempty"`
	Offerings []memberOfferingView `json:"offerings"`
}

type memberAuthView struct {
	Method       string `json:"method,omitempty"`
	SecretRefSet bool   `json:"secret_ref_set,omitempty"`
}

type memberOfferingView struct {
	CapabilityID    string `json:"capability_id"`
	OfferingID      string `json:"offering_id"`
	InteractionMode string `json:"interaction_mode"`
}

type offeringView struct {
	CapabilityID    string                `json:"capability_id"`
	OfferingID      string                `json:"offering_id"`
	InteractionMode string                `json:"interaction_mode"`
	BackendCount    int                   `json:"backend_count"`
	Backends        []offeringBackendView `json:"backends"`
}

type offeringBackendView struct {
	MemberEthAddress  string `json:"member_eth_address"`
	MemberDisplayName string `json:"member_display_name,omitempty"`
	BackendID         string `json:"backend_id"`
	Transport         string `json:"transport"`
	URL               string `json:"url,omitempty"`
}

type snapshotSummary struct {
	ID            string    `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	Source        string    `json:"source"`
	MemberCount   int       `json:"member_count"`
	RenderedBytes int       `json:"rendered_bytes"`
}

type workReceiptView struct {
	ID                string    `json:"id"`
	CreatedAt         time.Time `json:"created_at"`
	RoundID           string    `json:"round_id,omitempty"`
	RequestID         string    `json:"request_id"`
	CapabilityID      string    `json:"capability_id"`
	OfferingID        string    `json:"offering_id"`
	MemberEthAddress  string    `json:"member_eth_address"`
	BackendID         string    `json:"backend_id"`
	ActualUnits       uint64    `json:"actual_units,omitempty"`
	GatewayRevenueWei string    `json:"gateway_revenue_wei,omitempty"`
	Status            string    `json:"status"`
}

type roundReceiptView struct {
	ID               string                  `json:"id"`
	CreatedAt        time.Time               `json:"created_at"`
	RoundID          string                  `json:"round_id"`
	PoolRevenueWei   string                  `json:"pool_revenue_wei"`
	PoolCutWei       string                  `json:"pool_cut_wei"`
	DistributableWei string                  `json:"distributable_wei"`
	MemberPayouts    []memberRoundPayoutView `json:"member_payouts,omitempty"`
}

type memberRoundPayoutView struct {
	MemberEthAddress string `json:"member_eth_address"`
	ContributionWei  string `json:"contribution_wei"`
	SharePPM         uint64 `json:"share_ppm"`
	PayoutWei        string `json:"payout_wei"`
}

type payoutIntentView struct {
	ID                 string    `json:"id"`
	CreatedAt          time.Time `json:"created_at"`
	RoundReceiptID     string    `json:"round_receipt_id"`
	RoundID            string    `json:"round_id"`
	MemberEthAddress   string    `json:"member_eth_address"`
	DestinationAddress string    `json:"destination_address"`
	ChainID            uint64    `json:"chain_id"`
	Asset              string    `json:"asset"`
	AmountWei          string    `json:"amount_wei"`
	Status             string    `json:"status"`
	ExportedAt         time.Time `json:"exported_at,omitempty"`
	LeasedAt           time.Time `json:"leased_at,omitempty"`
	LeaseID            string    `json:"lease_id,omitempty"`
	LeaseOwner         string    `json:"lease_owner,omitempty"`
	LeaseExpiresAt     time.Time `json:"lease_expires_at,omitempty"`
	SubmittedAt        time.Time `json:"submitted_at,omitempty"`
	PaidAt             time.Time `json:"paid_at,omitempty"`
	FailedAt           time.Time `json:"failed_at,omitempty"`
	RetryCount         uint64    `json:"retry_count,omitempty"`
	LastRequeuedAt     time.Time `json:"last_requeued_at,omitempty"`
	ExternalRef        string    `json:"external_ref,omitempty"`
	TxHash             string    `json:"tx_hash,omitempty"`
	FailureReason      string    `json:"failure_reason,omitempty"`
}

type memberPayoutSummaryView struct {
	MemberEthAddress string    `json:"member_eth_address"`
	IntentCount      int       `json:"intent_count"`
	PendingWei       string    `json:"pending_wei"`
	ExportedWei      string    `json:"exported_wei"`
	LeasedWei        string    `json:"leased_wei"`
	SubmittedWei     string    `json:"submitted_wei"`
	PaidWei          string    `json:"paid_wei"`
	FailedWei        string    `json:"failed_wei"`
	RetriedCount     int       `json:"retried_count"`
	TotalRetryCount  uint64    `json:"total_retry_count"`
	LastRequeuedAt   time.Time `json:"last_requeued_at,omitempty"`
}

type roundPayoutSummaryView struct {
	RoundID         string    `json:"round_id"`
	RoundReceiptID  string    `json:"round_receipt_id"`
	IntentCount     int       `json:"intent_count"`
	MemberCount     int       `json:"member_count"`
	PendingCount    int       `json:"pending_count"`
	ExportedCount   int       `json:"exported_count"`
	LeasedCount     int       `json:"leased_count"`
	SubmittedCount  int       `json:"submitted_count"`
	PaidCount       int       `json:"paid_count"`
	FailedCount     int       `json:"failed_count"`
	PendingWei      string    `json:"pending_wei"`
	ExportedWei     string    `json:"exported_wei"`
	LeasedWei       string    `json:"leased_wei"`
	SubmittedWei    string    `json:"submitted_wei"`
	PaidWei         string    `json:"paid_wei"`
	FailedWei       string    `json:"failed_wei"`
	RetriedCount    int       `json:"retried_count"`
	TotalRetryCount uint64    `json:"total_retry_count"`
	LastRequeuedAt  time.Time `json:"last_requeued_at,omitempty"`
}

type payoutAlertView struct {
	Type              string           `json:"type"`
	Severity          string           `json:"severity"`
	Message           string           `json:"message"`
	Intent            payoutIntentView `json:"intent"`
	AgeSeconds        int64            `json:"age_seconds,omitempty"`
	RetryAfterSeconds int64            `json:"retry_after_seconds,omitempty"`
}

type payoutAlertSummaryView struct {
	AlertCount             int `json:"alert_count"`
	CriticalCount          int `json:"critical_count"`
	WarningCount           int `json:"warning_count"`
	SubmittedStaleCount    int `json:"submitted_stale_count"`
	FailedStaleCount       int `json:"failed_stale_count"`
	LeaseExpiringSoonCount int `json:"lease_expiring_soon_count"`
	RetryLimitCount        int `json:"retry_limit_count"`
	RecentRequeueCount     int `json:"recent_requeue_count"`
}

type publicRoundView struct {
	ID               string    `json:"id"`
	CreatedAt        time.Time `json:"created_at"`
	RoundID          string    `json:"round_id"`
	PoolRevenueWei   string    `json:"pool_revenue_wei"`
	PoolCutWei       string    `json:"pool_cut_wei"`
	DistributableWei string    `json:"distributable_wei"`
	MemberCount      int       `json:"member_count"`
}

type roundCloseRequest struct {
	ID                     string   `json:"id"`
	RoundID                string   `json:"round_id"`
	PoolRevenueWei         string   `json:"pool_revenue_wei"`
	PoolCutWei             string   `json:"pool_cut_wei"`
	IncludedWorkReceiptIDs []string `json:"included_work_receipt_ids"`
}

type payoutIntentDeriveRequest struct {
	RoundReceiptID string `json:"round_receipt_id,omitempty"`
	RoundID        string `json:"round_id,omitempty"`
}

type payoutIntentExportRequest struct {
	RoundID          string `json:"round_id,omitempty"`
	MemberEthAddress string `json:"member_eth_address,omitempty"`
	Status           string `json:"status,omitempty"`
	Format           string `json:"format,omitempty"`
}

type payoutIntentStatusUpdateRequest struct {
	IDs           []string `json:"ids"`
	Status        string   `json:"status"`
	LeaseID       string   `json:"lease_id,omitempty"`
	ExternalRef   string   `json:"external_ref,omitempty"`
	TxHash        string   `json:"tx_hash,omitempty"`
	FailureReason string   `json:"failure_reason,omitempty"`
}

type payoutIntentClaimRequest struct {
	ExecutorID       string `json:"executor_id"`
	LeaseTTLSeconds  int    `json:"lease_ttl_seconds,omitempty"`
	RoundID          string `json:"round_id,omitempty"`
	MemberEthAddress string `json:"member_eth_address,omitempty"`
	Limit            int    `json:"limit,omitempty"`
}

type payoutIntentRenewRequest struct {
	ExecutorID      string `json:"executor_id"`
	LeaseID         string `json:"lease_id"`
	LeaseTTLSeconds int    `json:"lease_ttl_seconds,omitempty"`
}

type payoutIntentReleaseRequest struct {
	ExecutorID string   `json:"executor_id"`
	LeaseID    string   `json:"lease_id"`
	IDs        []string `json:"ids,omitempty"`
}

type payoutIntentRequeueRequest struct {
	IDs []string `json:"ids"`
}

func buildMemberViews(cfg *config.Config) []memberView {
	if cfg == nil {
		return nil
	}
	out := make([]memberView, 0, len(cfg.Members))
	for _, member := range cfg.Members {
		view := memberView{
			EthAddress:  member.EthAddress,
			DisplayName: member.DisplayName,
			PayoutMode:  member.PayoutMode,
			Backends:    make([]memberBackendView, 0, len(member.Backends)),
		}
		for _, backend := range member.Backends {
			backendView := memberBackendView{
				ID:        backend.ID,
				Transport: backend.Transport,
				URL:       backend.URL,
				Offerings: make([]memberOfferingView, 0, len(backend.Offerings)),
			}
			if backend.Auth.Method != "" && backend.Auth.Method != "none" {
				backendView.Auth.Method = backend.Auth.Method
				backendView.Auth.SecretRefSet = backend.Auth.SecretRef != ""
			}
			for _, offering := range backend.Offerings {
				backendView.Offerings = append(backendView.Offerings, memberOfferingView{
					CapabilityID:    offering.CapabilityID,
					OfferingID:      offering.OfferingID,
					InteractionMode: offering.InteractionMode,
				})
			}
			view.Backends = append(view.Backends, backendView)
		}
		out = append(out, view)
	}
	return out
}

func buildOfferingViews(cfg *config.Config) []offeringView {
	if cfg == nil {
		return nil
	}
	grouped := map[string]*offeringView{}
	order := make([]string, 0)
	for _, member := range cfg.Members {
		for _, backend := range member.Backends {
			for _, offering := range backend.Offerings {
				key := offering.CapabilityID + "\x00" + offering.OfferingID
				view, ok := grouped[key]
				if !ok {
					view = &offeringView{
						CapabilityID:    offering.CapabilityID,
						OfferingID:      offering.OfferingID,
						InteractionMode: offering.InteractionMode,
						Backends:        make([]offeringBackendView, 0, 1),
					}
					grouped[key] = view
					order = append(order, key)
				}
				view.Backends = append(view.Backends, offeringBackendView{
					MemberEthAddress:  member.EthAddress,
					MemberDisplayName: member.DisplayName,
					BackendID:         backend.ID,
					Transport:         backend.Transport,
					URL:               backend.URL,
				})
			}
		}
	}
	out := make([]offeringView, 0, len(order))
	for _, key := range order {
		view := grouped[key]
		view.BackendCount = len(view.Backends)
		out = append(out, *view)
	}
	return out
}

func summarizeSnapshot(s *repo.Snapshot) *snapshotSummary {
	if s == nil {
		return nil
	}
	return &snapshotSummary{
		ID:            s.ID,
		CreatedAt:     s.CreatedAt,
		Source:        s.Source,
		MemberCount:   s.MemberCount,
		RenderedBytes: s.RenderedBytes,
	}
}

func summarizeSnapshots(items []repo.Snapshot) []snapshotSummary {
	out := make([]snapshotSummary, 0, len(items))
	for _, item := range items {
		out = append(out, snapshotSummary{
			ID:            item.ID,
			CreatedAt:     item.CreatedAt,
			Source:        item.Source,
			MemberCount:   item.MemberCount,
			RenderedBytes: item.RenderedBytes,
		})
	}
	return out
}

func buildWorkReceiptViews(items []types.WorkReceipt) []workReceiptView {
	out := make([]workReceiptView, 0, len(items))
	for _, item := range items {
		out = append(out, workReceiptView{
			ID:                item.ID,
			CreatedAt:         item.CreatedAt,
			RoundID:           item.RoundID,
			RequestID:         item.RequestID,
			CapabilityID:      item.CapabilityID,
			OfferingID:        item.OfferingID,
			MemberEthAddress:  item.MemberEthAddress,
			BackendID:         item.BackendID,
			ActualUnits:       item.ActualUnits,
			GatewayRevenueWei: item.GatewayRevenueWei,
			Status:            item.Status,
		})
	}
	return out
}

func buildRoundReceiptViews(items []types.RoundReceipt) []roundReceiptView {
	out := make([]roundReceiptView, 0, len(items))
	for _, item := range items {
		view := roundReceiptView{
			ID:               item.ID,
			CreatedAt:        item.CreatedAt,
			RoundID:          item.RoundID,
			PoolRevenueWei:   item.PoolRevenueWei,
			PoolCutWei:       item.PoolCutWei,
			DistributableWei: item.DistributableWei,
			MemberPayouts:    make([]memberRoundPayoutView, 0, len(item.MemberPayouts)),
		}
		for _, payout := range item.MemberPayouts {
			view.MemberPayouts = append(view.MemberPayouts, memberRoundPayoutView{
				MemberEthAddress: payout.MemberEthAddress,
				ContributionWei:  payout.ContributionWei,
				SharePPM:         payout.SharePPM,
				PayoutWei:        payout.PayoutWei,
			})
		}
		out = append(out, view)
	}
	return out
}

func buildPayoutIntentViews(items []types.PayoutIntent) []payoutIntentView {
	out := make([]payoutIntentView, 0, len(items))
	for _, item := range items {
		out = append(out, payoutIntentView{
			ID:                 item.ID,
			CreatedAt:          item.CreatedAt,
			RoundReceiptID:     item.RoundReceiptID,
			RoundID:            item.RoundID,
			MemberEthAddress:   item.MemberEthAddress,
			DestinationAddress: item.DestinationAddress,
			ChainID:            item.ChainID,
			Asset:              item.Asset,
			AmountWei:          item.AmountWei,
			Status:             item.Status,
			ExportedAt:         item.ExportedAt,
			LeasedAt:           item.LeasedAt,
			LeaseID:            item.LeaseID,
			LeaseOwner:         item.LeaseOwner,
			LeaseExpiresAt:     item.LeaseExpiresAt,
			SubmittedAt:        item.SubmittedAt,
			PaidAt:             item.PaidAt,
			FailedAt:           item.FailedAt,
			RetryCount:         item.RetryCount,
			LastRequeuedAt:     item.LastRequeuedAt,
			ExternalRef:        item.ExternalRef,
			TxHash:             item.TxHash,
			FailureReason:      item.FailureReason,
		})
	}
	return out
}

func buildMemberPayoutSummaryViews(items []types.PayoutIntent) []memberPayoutSummaryView {
	type accum struct {
		IntentCount     int
		PendingWei      *big.Int
		ExportedWei     *big.Int
		LeasedWei       *big.Int
		SubmittedWei    *big.Int
		PaidWei         *big.Int
		FailedWei       *big.Int
		RetriedCount    int
		TotalRetryCount uint64
		LastRequeuedAt  time.Time
	}
	grouped := map[string]*accum{}
	order := make([]string, 0)
	for _, item := range items {
		member := item.MemberEthAddress
		if _, ok := grouped[member]; !ok {
			grouped[member] = &accum{
				PendingWei:   big.NewInt(0),
				ExportedWei:  big.NewInt(0),
				LeasedWei:    big.NewInt(0),
				SubmittedWei: big.NewInt(0),
				PaidWei:      big.NewInt(0),
				FailedWei:    big.NewInt(0),
			}
			order = append(order, member)
		}
		amount, ok := new(big.Int).SetString(item.AmountWei, 10)
		if !ok {
			amount = big.NewInt(0)
		}
		acc := grouped[member]
		acc.IntentCount++
		if item.RetryCount > 0 {
			acc.RetriedCount++
			acc.TotalRetryCount += item.RetryCount
			if item.LastRequeuedAt.After(acc.LastRequeuedAt) {
				acc.LastRequeuedAt = item.LastRequeuedAt
			}
		}
		switch item.Status {
		case "pending":
			acc.PendingWei.Add(acc.PendingWei, amount)
		case "exported":
			acc.ExportedWei.Add(acc.ExportedWei, amount)
		case "leased":
			acc.LeasedWei.Add(acc.LeasedWei, amount)
		case "submitted":
			acc.SubmittedWei.Add(acc.SubmittedWei, amount)
		case "paid":
			acc.PaidWei.Add(acc.PaidWei, amount)
		case "failed":
			acc.FailedWei.Add(acc.FailedWei, amount)
		}
	}
	out := make([]memberPayoutSummaryView, 0, len(order))
	for _, member := range order {
		acc := grouped[member]
		out = append(out, memberPayoutSummaryView{
			MemberEthAddress: member,
			IntentCount:      acc.IntentCount,
			PendingWei:       acc.PendingWei.String(),
			ExportedWei:      acc.ExportedWei.String(),
			LeasedWei:        acc.LeasedWei.String(),
			SubmittedWei:     acc.SubmittedWei.String(),
			PaidWei:          acc.PaidWei.String(),
			FailedWei:        acc.FailedWei.String(),
			RetriedCount:     acc.RetriedCount,
			TotalRetryCount:  acc.TotalRetryCount,
			LastRequeuedAt:   acc.LastRequeuedAt,
		})
	}
	return out
}

func buildRoundPayoutSummaryViews(items []types.PayoutIntent) []roundPayoutSummaryView {
	type accum struct {
		RoundReceiptID  string
		MemberSet       map[string]struct{}
		IntentCount     int
		PendingCount    int
		ExportedCount   int
		LeasedCount     int
		SubmittedCount  int
		PaidCount       int
		FailedCount     int
		PendingWei      *big.Int
		ExportedWei     *big.Int
		LeasedWei       *big.Int
		SubmittedWei    *big.Int
		PaidWei         *big.Int
		FailedWei       *big.Int
		RetriedCount    int
		TotalRetryCount uint64
		LastRequeuedAt  time.Time
	}
	grouped := map[string]*accum{}
	order := make([]string, 0)
	for _, item := range items {
		roundID := item.RoundID
		if _, ok := grouped[roundID]; !ok {
			grouped[roundID] = &accum{
				RoundReceiptID: item.RoundReceiptID,
				MemberSet:      map[string]struct{}{},
				PendingWei:     big.NewInt(0),
				ExportedWei:    big.NewInt(0),
				LeasedWei:      big.NewInt(0),
				SubmittedWei:   big.NewInt(0),
				PaidWei:        big.NewInt(0),
				FailedWei:      big.NewInt(0),
			}
			order = append(order, roundID)
		}
		amount, ok := new(big.Int).SetString(item.AmountWei, 10)
		if !ok {
			amount = big.NewInt(0)
		}
		acc := grouped[roundID]
		acc.IntentCount++
		acc.MemberSet[item.MemberEthAddress] = struct{}{}
		if item.RetryCount > 0 {
			acc.RetriedCount++
			acc.TotalRetryCount += item.RetryCount
			if item.LastRequeuedAt.After(acc.LastRequeuedAt) {
				acc.LastRequeuedAt = item.LastRequeuedAt
			}
		}
		switch item.Status {
		case "pending":
			acc.PendingCount++
			acc.PendingWei.Add(acc.PendingWei, amount)
		case "exported":
			acc.ExportedCount++
			acc.ExportedWei.Add(acc.ExportedWei, amount)
		case "leased":
			acc.LeasedCount++
			acc.LeasedWei.Add(acc.LeasedWei, amount)
		case "submitted":
			acc.SubmittedCount++
			acc.SubmittedWei.Add(acc.SubmittedWei, amount)
		case "paid":
			acc.PaidCount++
			acc.PaidWei.Add(acc.PaidWei, amount)
		case "failed":
			acc.FailedCount++
			acc.FailedWei.Add(acc.FailedWei, amount)
		}
	}
	out := make([]roundPayoutSummaryView, 0, len(order))
	for _, roundID := range order {
		acc := grouped[roundID]
		out = append(out, roundPayoutSummaryView{
			RoundID:         roundID,
			RoundReceiptID:  acc.RoundReceiptID,
			IntentCount:     acc.IntentCount,
			MemberCount:     len(acc.MemberSet),
			PendingCount:    acc.PendingCount,
			ExportedCount:   acc.ExportedCount,
			LeasedCount:     acc.LeasedCount,
			SubmittedCount:  acc.SubmittedCount,
			PaidCount:       acc.PaidCount,
			FailedCount:     acc.FailedCount,
			PendingWei:      acc.PendingWei.String(),
			ExportedWei:     acc.ExportedWei.String(),
			LeasedWei:       acc.LeasedWei.String(),
			SubmittedWei:    acc.SubmittedWei.String(),
			PaidWei:         acc.PaidWei.String(),
			FailedWei:       acc.FailedWei.String(),
			RetriedCount:    acc.RetriedCount,
			TotalRetryCount: acc.TotalRetryCount,
			LastRequeuedAt:  acc.LastRequeuedAt,
		})
	}
	return out
}

type payoutAlertThresholds struct {
	SubmittedOlderThan  time.Duration
	FailedOlderThan     time.Duration
	LeaseExpiresWithin  time.Duration
	RetryCountAtLeast   uint64
	RecentRequeueWithin time.Duration
}

func buildPayoutAlertViews(items []types.PayoutIntent, now time.Time, thresholds payoutAlertThresholds) []payoutAlertView {
	alerts := make([]payoutAlertView, 0)
	for _, item := range items {
		intentView := buildPayoutIntentViews([]types.PayoutIntent{item})[0]
		switch item.Status {
		case "submitted":
			if !item.SubmittedAt.IsZero() {
				age := now.Sub(item.SubmittedAt)
				if age >= thresholds.SubmittedOlderThan {
					alerts = append(alerts, payoutAlertView{
						Type:       "submitted_stale",
						Severity:   "critical",
						Message:    fmt.Sprintf("payout intent has been submitted for %s without confirmation", age.Truncate(time.Second)),
						Intent:     intentView,
						AgeSeconds: int64(age.Seconds()),
					})
				}
			}
		case "failed":
			failedSince := payoutIntentFailureAnchor(item)
			age := now.Sub(failedSince)
			if age >= thresholds.FailedOlderThan {
				alerts = append(alerts, payoutAlertView{
					Type:       "failed_stale",
					Severity:   "warning",
					Message:    fmt.Sprintf("payout intent has remained failed for %s", age.Truncate(time.Second)),
					Intent:     intentView,
					AgeSeconds: int64(age.Seconds()),
				})
			}
			if thresholds.RetryCountAtLeast > 0 && item.RetryCount >= thresholds.RetryCountAtLeast {
				alerts = append(alerts, payoutAlertView{
					Type:       "retry_limit_reached",
					Severity:   "critical",
					Message:    fmt.Sprintf("payout intent has been requeued %d times", item.RetryCount),
					Intent:     intentView,
					AgeSeconds: int64(age.Seconds()),
				})
			}
			if thresholds.RecentRequeueWithin > 0 && !item.LastRequeuedAt.IsZero() {
				sinceRequeue := now.Sub(item.LastRequeuedAt)
				if sinceRequeue <= thresholds.RecentRequeueWithin {
					alerts = append(alerts, payoutAlertView{
						Type:       "failed_after_recent_requeue",
						Severity:   "warning",
						Message:    fmt.Sprintf("payout intent failed again %s after requeue", sinceRequeue.Truncate(time.Second)),
						Intent:     intentView,
						AgeSeconds: int64(sinceRequeue.Seconds()),
					})
				}
			}
		case "leased":
			if !item.LeaseExpiresAt.IsZero() {
				remaining := item.LeaseExpiresAt.Sub(now)
				if remaining <= thresholds.LeaseExpiresWithin {
					severity := "warning"
					if remaining <= 15*time.Second {
						severity = "critical"
					}
					retryAfterSeconds := int64(remaining.Seconds())
					if retryAfterSeconds < 0 {
						retryAfterSeconds = 0
					}
					alerts = append(alerts, payoutAlertView{
						Type:              "lease_expiring_soon",
						Severity:          severity,
						Message:           fmt.Sprintf("payout lease expires in %s", remaining.Truncate(time.Second)),
						Intent:            intentView,
						RetryAfterSeconds: retryAfterSeconds,
					})
				}
			}
		}
	}
	return alerts
}

func summarizePayoutAlerts(items []payoutAlertView) payoutAlertSummaryView {
	out := payoutAlertSummaryView{AlertCount: len(items)}
	for _, item := range items {
		switch item.Severity {
		case "critical":
			out.CriticalCount++
		case "warning":
			out.WarningCount++
		}
		switch item.Type {
		case "submitted_stale":
			out.SubmittedStaleCount++
		case "failed_stale":
			out.FailedStaleCount++
		case "lease_expiring_soon":
			out.LeaseExpiringSoonCount++
		case "retry_limit_reached":
			out.RetryLimitCount++
		case "failed_after_recent_requeue":
			out.RecentRequeueCount++
		}
	}
	return out
}

func payoutIntentFailureAnchor(item types.PayoutIntent) time.Time {
	switch {
	case !item.FailedAt.IsZero():
		return item.FailedAt
	case !item.SubmittedAt.IsZero():
		return item.SubmittedAt
	case !item.ExportedAt.IsZero():
		return item.ExportedAt
	default:
		return item.CreatedAt
	}
}

func buildPublicRoundViews(items []types.RoundReceipt) []publicRoundView {
	out := make([]publicRoundView, 0, len(items))
	for _, item := range items {
		out = append(out, publicRoundView{
			ID:               item.ID,
			CreatedAt:        item.CreatedAt,
			RoundID:          item.RoundID,
			PoolRevenueWei:   item.PoolRevenueWei,
			PoolCutWei:       item.PoolCutWei,
			DistributableWei: item.DistributableWei,
			MemberCount:      len(item.MemberPayouts),
		})
	}
	return out
}

func validateWorkReceipt(receipt types.WorkReceipt) error {
	if receipt.ID == "" {
		return errors.New("work receipt id is required")
	}
	if receipt.RequestID == "" {
		return errors.New("work receipt request_id is required")
	}
	if receipt.CapabilityID == "" {
		return errors.New("work receipt capability_id is required")
	}
	if receipt.OfferingID == "" {
		return errors.New("work receipt offering_id is required")
	}
	if receipt.MemberEthAddress == "" {
		return errors.New("work receipt member_eth_address is required")
	}
	if receipt.BackendID == "" {
		return errors.New("work receipt backend_id is required")
	}
	switch receipt.Status {
	case "stub":
		if receipt.ActualUnits != 0 {
			return errors.New("work receipt status=stub must not set actual_units")
		}
	case "final":
		if receipt.ActualUnits == 0 {
			return errors.New("work receipt status=final requires actual_units > 0")
		}
	default:
		return errors.New(`work receipt status must be "stub" or "final"`)
	}
	return nil
}

func validateRoundReceipt(receipt types.RoundReceipt) error {
	if receipt.ID == "" {
		return errors.New("round receipt id is required")
	}
	if receipt.RoundID == "" {
		return errors.New("round receipt round_id is required")
	}
	if receipt.PoolRevenueWei == "" {
		return errors.New("round receipt pool_revenue_wei is required")
	}
	if receipt.PoolCutWei == "" {
		return errors.New("round receipt pool_cut_wei is required")
	}
	if receipt.DistributableWei == "" {
		return errors.New("round receipt distributable_wei is required")
	}
	return nil
}

func validateRoundCloseRequest(req roundCloseRequest) error {
	if req.ID == "" {
		return errors.New("round close id is required")
	}
	if req.RoundID == "" {
		return errors.New("round close round_id is required")
	}
	if req.PoolRevenueWei == "" {
		return errors.New("round close pool_revenue_wei is required")
	}
	if req.PoolCutWei == "" {
		return errors.New("round close pool_cut_wei is required")
	}
	if len(req.IncludedWorkReceiptIDs) == 0 {
		return errors.New("round close included_work_receipt_ids must not be empty")
	}
	return nil
}

func validatePayoutIntentDeriveRequest(req payoutIntentDeriveRequest) error {
	if strings.TrimSpace(req.RoundReceiptID) == "" && strings.TrimSpace(req.RoundID) == "" {
		return errors.New("payout intent derive requires round_receipt_id or round_id")
	}
	return nil
}

func validatePayoutIntentClaimRequest(req payoutIntentClaimRequest) error {
	if strings.TrimSpace(req.ExecutorID) == "" {
		return errors.New("payout intent claim requires executor_id")
	}
	if req.LeaseTTLSeconds <= 0 {
		return errors.New("payout intent claim requires lease_ttl_seconds > 0")
	}
	if req.Limit < 0 {
		return errors.New("payout intent claim limit must be >= 0")
	}
	return nil
}

func validatePayoutIntentRenewRequest(req payoutIntentRenewRequest) error {
	if strings.TrimSpace(req.ExecutorID) == "" {
		return errors.New("payout intent renew requires executor_id")
	}
	if strings.TrimSpace(req.LeaseID) == "" {
		return errors.New("payout intent renew requires lease_id")
	}
	if req.LeaseTTLSeconds <= 0 {
		return errors.New("payout intent renew requires lease_ttl_seconds > 0")
	}
	return nil
}

func validatePayoutIntentReleaseRequest(req payoutIntentReleaseRequest) error {
	if strings.TrimSpace(req.ExecutorID) == "" {
		return errors.New("payout intent release requires executor_id")
	}
	if strings.TrimSpace(req.LeaseID) == "" {
		return errors.New("payout intent release requires lease_id")
	}
	return nil
}

func validatePayoutIntentRequeueRequest(req payoutIntentRequeueRequest) error {
	if len(req.IDs) == 0 {
		return errors.New("payout intent requeue requires ids")
	}
	return nil
}

func validatePayoutIntentStatusUpdateRequest(req payoutIntentStatusUpdateRequest) error {
	if len(req.IDs) == 0 {
		return errors.New("payout intent status update requires ids")
	}
	switch strings.TrimSpace(req.Status) {
	case "submitted", "paid", "failed":
	case "":
		return errors.New("payout intent status update requires status")
	default:
		return errors.New(`payout intent status must be "submitted", "paid", or "failed"`)
	}
	if strings.TrimSpace(req.Status) == "failed" && strings.TrimSpace(req.FailureReason) == "" {
		return errors.New("payout intent status=failed requires failure_reason")
	}
	return nil
}

func loadRoundReceiptForPayouts(stateRepo *repo.StateRepo, req payoutIntentDeriveRequest) (types.RoundReceipt, error) {
	if strings.TrimSpace(req.RoundReceiptID) != "" {
		return stateRepo.GetRoundReceipt(strings.TrimSpace(req.RoundReceiptID))
	}
	return stateRepo.FindLatestRoundReceiptByRoundID(strings.TrimSpace(req.RoundID))
}

func buildPayoutIntents(roundReceipt types.RoundReceipt) []types.PayoutIntent {
	intents := make([]types.PayoutIntent, 0, len(roundReceipt.MemberPayouts))
	now := time.Now().UTC()
	for _, payout := range roundReceipt.MemberPayouts {
		intents = append(intents, types.PayoutIntent{
			ID:                 fmt.Sprintf("payout-%s-%s", roundReceipt.RoundID, payout.MemberEthAddress),
			CreatedAt:          now,
			RoundReceiptID:     roundReceipt.ID,
			RoundID:            roundReceipt.RoundID,
			MemberEthAddress:   payout.MemberEthAddress,
			DestinationAddress: payout.MemberEthAddress,
			ChainID:            42161,
			Asset:              "native_eth",
			AmountWei:          payout.PayoutWei,
			Status:             "pending",
		})
	}
	return intents
}

func applyPayoutIntentStatus(intent types.PayoutIntent, status string, leaseID string, externalRef string, txHash string, failureReason string, now time.Time) (types.PayoutIntent, error) {
	switch status {
	case "submitted":
		if intent.Status == "leased" {
			if strings.TrimSpace(leaseID) == "" || intent.LeaseID != strings.TrimSpace(leaseID) {
				return types.PayoutIntent{}, fmt.Errorf("payout intent %q requires matching lease_id for submitted", intent.ID)
			}
		} else if intent.Status != "exported" && intent.Status != "failed" {
			return types.PayoutIntent{}, fmt.Errorf("payout intent %q cannot move from %q to submitted", intent.ID, intent.Status)
		}
		intent.Status = "submitted"
		intent.SubmittedAt = now
		intent.LeasedAt = time.Time{}
		intent.LeaseID = ""
		intent.LeaseOwner = ""
		intent.LeaseExpiresAt = time.Time{}
		intent.FailedAt = time.Time{}
		if externalRef != "" {
			intent.ExternalRef = externalRef
		}
		if txHash != "" {
			intent.TxHash = txHash
		}
		intent.FailureReason = ""
	case "paid":
		if intent.Status != "submitted" {
			return types.PayoutIntent{}, fmt.Errorf("payout intent %q cannot move from %q to paid", intent.ID, intent.Status)
		}
		intent.Status = "paid"
		intent.PaidAt = now
		intent.FailedAt = time.Time{}
		if externalRef != "" {
			intent.ExternalRef = externalRef
		}
		if txHash != "" {
			intent.TxHash = txHash
		}
		intent.FailureReason = ""
	case "failed":
		if intent.Status == "leased" {
			if strings.TrimSpace(leaseID) == "" || intent.LeaseID != strings.TrimSpace(leaseID) {
				return types.PayoutIntent{}, fmt.Errorf("payout intent %q requires matching lease_id for failed", intent.ID)
			}
		} else if intent.Status != "submitted" && intent.Status != "exported" {
			return types.PayoutIntent{}, fmt.Errorf("payout intent %q cannot move from %q to failed", intent.ID, intent.Status)
		}
		intent.Status = "failed"
		intent.FailedAt = now
		intent.LeasedAt = time.Time{}
		intent.LeaseID = ""
		intent.LeaseOwner = ""
		intent.LeaseExpiresAt = time.Time{}
		if externalRef != "" {
			intent.ExternalRef = externalRef
		}
		if txHash != "" {
			intent.TxHash = txHash
		}
		intent.FailureReason = failureReason
	default:
		return types.PayoutIntent{}, fmt.Errorf("unsupported payout intent status %q", status)
	}
	return intent, nil
}

func requeuePayoutIntent(intent types.PayoutIntent, now time.Time) (types.PayoutIntent, error) {
	if intent.Status != "failed" {
		return types.PayoutIntent{}, fmt.Errorf("payout intent %q cannot be requeued from %q", intent.ID, intent.Status)
	}
	intent.Status = "exported"
	intent.ExportedAt = now
	intent.LeasedAt = time.Time{}
	intent.LeaseID = ""
	intent.LeaseOwner = ""
	intent.LeaseExpiresAt = time.Time{}
	intent.SubmittedAt = time.Time{}
	intent.PaidAt = time.Time{}
	intent.FailedAt = time.Time{}
	intent.RetryCount++
	intent.LastRequeuedAt = now
	intent.ExternalRef = ""
	intent.TxHash = ""
	intent.FailureReason = ""
	return intent, nil
}

func releaseExpiredLease(intent types.PayoutIntent, now time.Time) (types.PayoutIntent, bool) {
	if intent.Status != "leased" || intent.LeaseExpiresAt.IsZero() || intent.LeaseExpiresAt.After(now) {
		return intent, false
	}
	intent.Status = "exported"
	intent.LeasedAt = time.Time{}
	intent.LeaseID = ""
	intent.LeaseOwner = ""
	intent.LeaseExpiresAt = time.Time{}
	return intent, true
}

func normalizeExpiredPayoutIntentLeases(stateRepo *repo.StateRepo, items []types.PayoutIntent, now time.Time) ([]types.PayoutIntent, error) {
	out := make([]types.PayoutIntent, 0, len(items))
	for _, item := range items {
		next, changed := releaseExpiredLease(item, now)
		if changed {
			if err := stateRepo.SavePayoutIntent(next); err != nil {
				return nil, err
			}
		}
		out = append(out, next)
	}
	return out, nil
}

func filterPayoutIntents(items []types.PayoutIntent, roundID, memberEthAddress, status string) []types.PayoutIntent {
	roundID = strings.TrimSpace(roundID)
	memberEthAddress = strings.TrimSpace(memberEthAddress)
	status = strings.TrimSpace(status)
	filtered := make([]types.PayoutIntent, 0, len(items))
	for _, item := range items {
		if roundID != "" && item.RoundID != roundID {
			continue
		}
		if memberEthAddress != "" && item.MemberEthAddress != memberEthAddress {
			continue
		}
		if status != "" && item.Status != status {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func writePayoutIntentCSV(w http.ResponseWriter, items []types.PayoutIntent) {
	w.Header().Set("Content-Type", "text/csv")
	w.WriteHeader(http.StatusOK)
	buf := &bytes.Buffer{}
	cw := csv.NewWriter(buf)
	_ = cw.Write([]string{"id", "round_receipt_id", "round_id", "member_eth_address", "amount_wei", "status", "created_at", "exported_at", "leased_at", "lease_id", "lease_owner", "lease_expires_at", "submitted_at", "paid_at", "failed_at", "retry_count", "last_requeued_at", "external_ref", "tx_hash", "failure_reason"})
	for _, item := range items {
		_ = cw.Write([]string{
			item.ID,
			item.RoundReceiptID,
			item.RoundID,
			item.MemberEthAddress,
			item.AmountWei,
			item.Status,
			item.CreatedAt.Format(time.RFC3339Nano),
			item.ExportedAt.Format(time.RFC3339Nano),
			item.LeasedAt.Format(time.RFC3339Nano),
			item.LeaseID,
			item.LeaseOwner,
			item.LeaseExpiresAt.Format(time.RFC3339Nano),
			item.SubmittedAt.Format(time.RFC3339Nano),
			item.PaidAt.Format(time.RFC3339Nano),
			item.FailedAt.Format(time.RFC3339Nano),
			strconv.FormatUint(item.RetryCount, 10),
			item.LastRequeuedAt.Format(time.RFC3339Nano),
			item.ExternalRef,
			item.TxHash,
			item.FailureReason,
		})
	}
	cw.Flush()
	_, _ = w.Write(buf.Bytes())
}

func buildRoundReceiptFromCloseRequest(req roundCloseRequest, workReceipts []types.WorkReceipt) (types.RoundReceipt, error) {
	poolRevenue, ok := new(big.Int).SetString(req.PoolRevenueWei, 10)
	if !ok {
		return types.RoundReceipt{}, errors.New("round close pool_revenue_wei must be a decimal string")
	}
	poolCut, ok := new(big.Int).SetString(req.PoolCutWei, 10)
	if !ok {
		return types.RoundReceipt{}, errors.New("round close pool_cut_wei must be a decimal string")
	}
	if poolCut.Sign() < 0 || poolRevenue.Sign() < 0 {
		return types.RoundReceipt{}, errors.New("round close monetary values must be non-negative")
	}
	if poolCut.Cmp(poolRevenue) > 0 {
		return types.RoundReceipt{}, errors.New("round close pool_cut_wei must be <= pool_revenue_wei")
	}
	distributable := new(big.Int).Sub(poolRevenue, poolCut)

	memberContribs := make(map[string]*big.Int)
	totalContribution := big.NewInt(0)
	for _, receipt := range workReceipts {
		if receipt.Status != "final" {
			return types.RoundReceipt{}, fmt.Errorf("work receipt %q is not final", receipt.ID)
		}
		if receipt.MemberEthAddress == "" {
			return types.RoundReceipt{}, fmt.Errorf("work receipt %q missing member_eth_address", receipt.ID)
		}
		rev, ok := new(big.Int).SetString(receipt.GatewayRevenueWei, 10)
		if !ok {
			return types.RoundReceipt{}, fmt.Errorf("work receipt %q has invalid gateway_revenue_wei", receipt.ID)
		}
		if _, ok := memberContribs[receipt.MemberEthAddress]; !ok {
			memberContribs[receipt.MemberEthAddress] = big.NewInt(0)
		}
		memberContribs[receipt.MemberEthAddress].Add(memberContribs[receipt.MemberEthAddress], rev)
		totalContribution.Add(totalContribution, rev)
	}
	if totalContribution.Sign() <= 0 {
		return types.RoundReceipt{}, errors.New("round close total contribution must be > 0")
	}

	payouts := make([]types.MemberRoundPayout, 0, len(memberContribs))
	for member, contribution := range memberContribs {
		sharePPM := new(big.Int).Mul(contribution, big.NewInt(1_000_000))
		sharePPM.Quo(sharePPM, totalContribution)

		payoutWei := new(big.Int).Mul(distributable, contribution)
		payoutWei.Quo(payoutWei, totalContribution)

		payouts = append(payouts, types.MemberRoundPayout{
			MemberEthAddress: member,
			ContributionWei:  contribution.String(),
			SharePPM:         sharePPM.Uint64(),
			PayoutWei:        payoutWei.String(),
		})
	}

	return types.RoundReceipt{
		ID:                     req.ID,
		CreatedAt:              time.Now().UTC(),
		RoundID:                req.RoundID,
		PoolRevenueWei:         req.PoolRevenueWei,
		PoolCutWei:             req.PoolCutWei,
		DistributableWei:       distributable.String(),
		IncludedWorkReceiptIDs: append([]string(nil), req.IncludedWorkReceiptIDs...),
		MemberPayouts:          payouts,
	}, nil
}

func usageError(w io.Writer) error {
	_, _ = fmt.Fprintln(w, "usage: livepeer-pool-controller <generate-broker-config|serve|version> [flags]")
	return errors.New("invalid command")
}
