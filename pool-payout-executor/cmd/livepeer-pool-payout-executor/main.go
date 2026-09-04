package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"time"

	"net/http"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/config"
	ethclientx "github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/ethclient"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/observability"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/poolcontroller"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/repo"
	ethcommon "github.com/ethereum/go-ethereum/common"
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
	case "list-intents":
		return runListIntents(args[1:], stdout)
	case "prepare-batch":
		return runPrepareBatch(args[1:], stdout)
	case "send-native-batch":
		return runSendNativeBatch(args[1:], stdout)
	case "confirm-submitted":
		return runConfirmSubmitted(args[1:], stdout)
	case "list-alerts":
		return runListAlerts(args[1:], stdout)
	case "requeue-failed":
		return runRequeueFailed(args[1:], stdout)
	case "requeue-alerted-failed":
		return runRequeueAlertedFailed(args[1:], stdout)
	case "reconcile-once":
		return runReconcileOnce(args[1:], stdout)
	case "reconcile-loop":
		return runReconcileLoop(args[1:], stdout)
	case "state-summary":
		return runStateSummary(args[1:], stdout)
	case "mark-submitted":
		return runMarkStatus(args[1:], stdout, "submitted")
	case "mark-paid":
		return runMarkStatus(args[1:], stdout, "paid")
	case "mark-failed":
		return runMarkStatus(args[1:], stdout, "failed")
	case "version":
		_, err := fmt.Fprintln(stdout, version)
		return err
	default:
		return usageError(stderr)
	}
}

func runListIntents(args []string, stdout io.Writer) error {
	cfg, opts, err := loadListOptions(args, "list-intents")
	if err != nil {
		return err
	}
	client, err := poolcontroller.NewClient(cfg.PoolController)
	if err != nil {
		return err
	}
	items, err := client.ListPayoutIntents(context.Background(), opts)
	if err != nil {
		return err
	}
	return writeJSON(stdout, struct {
		Intents []poolcontroller.PayoutIntent `json:"intents"`
	}{Intents: items})
}

func runPrepareBatch(args []string, stdout io.Writer) error {
	cfg, opts, err := loadListOptions(args, "prepare-batch")
	if err != nil {
		return err
	}
	if opts.Status == "" {
		opts.Status = "exported"
	}
	if opts.Limit == 0 {
		opts.Limit = cfg.Executor.BatchSize
	}
	client, err := poolcontroller.NewClient(cfg.PoolController)
	if err != nil {
		return err
	}
	items, err := client.ListPayoutIntents(context.Background(), opts)
	if err != nil {
		return err
	}
	total := "0"
	for _, item := range items {
		total = addDecimalStrings(total, item.AmountWei)
	}
	return writeJSON(stdout, struct {
		Count     int                           `json:"count"`
		Status    string                        `json:"status"`
		TotalWei  string                        `json:"total_wei"`
		BatchSize int                           `json:"batch_size"`
		Payouts   []poolcontroller.PayoutIntent `json:"payouts"`
	}{
		Count:     len(items),
		Status:    opts.Status,
		TotalWei:  total,
		BatchSize: cfg.Executor.BatchSize,
		Payouts:   items,
	})
}

func runRequeueFailed(args []string, stdout io.Writer) error {
	cfg, opts, err := loadListOptions(args, "requeue-failed")
	if err != nil {
		return err
	}
	if opts.Status == "" {
		opts.Status = "failed"
	}
	client, err := poolcontroller.NewClient(cfg.PoolController)
	if err != nil {
		return err
	}
	items, err := client.ListPayoutIntents(context.Background(), opts)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	if len(ids) == 0 {
		return writeJSON(stdout, struct {
			Status  string                        `json:"status"`
			Count   int                           `json:"count"`
			Intents []poolcontroller.PayoutIntent `json:"intents"`
		}{
			Status:  "requeued",
			Count:   0,
			Intents: nil,
		})
	}
	requeued, err := client.RequeuePayoutIntents(context.Background(), poolcontroller.RequeuePayoutIntentsRequest{
		IDs: ids,
	})
	if err != nil {
		return err
	}
	return writeJSON(stdout, struct {
		Status  string                        `json:"status"`
		Count   int                           `json:"count"`
		Intents []poolcontroller.PayoutIntent `json:"intents"`
	}{
		Status:  "requeued",
		Count:   len(requeued),
		Intents: requeued,
	})
}

func runRequeueAlertedFailed(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("requeue-alerted-failed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to pool-payout-executor config")
	roundID := fs.String("round-id", "", "optional round ID filter")
	memberEthAddress := fs.String("member-eth-address", "", "optional member address filter")
	limit := fs.Int("limit", 100, "optional max number of alerts")
	failedOlderThanSeconds := fs.Int("failed-older-than-seconds", 3600, "failed stale threshold")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfigPath(*configPath)
	if err != nil {
		return err
	}
	client, err := poolcontroller.NewClient(cfg.PoolController)
	if err != nil {
		return err
	}
	_, alerts, err := client.ListPayoutAlerts(context.Background(), poolcontroller.ListPayoutAlertsOptions{
		RoundID:                strings.TrimSpace(*roundID),
		MemberEthAddress:       strings.TrimSpace(*memberEthAddress),
		Status:                 "failed",
		Limit:                  *limit,
		FailedOlderThanSeconds: *failedOlderThanSeconds,
	})
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(alerts))
	for _, alert := range alerts {
		if alert.Type != "failed_stale" {
			continue
		}
		ids = append(ids, alert.Intent.ID)
	}
	if len(ids) == 0 {
		return writeJSON(stdout, struct {
			Status  string                        `json:"status"`
			Count   int                           `json:"count"`
			Intents []poolcontroller.PayoutIntent `json:"intents"`
		}{
			Status:  "requeued",
			Count:   0,
			Intents: nil,
		})
	}
	requeued, err := client.RequeuePayoutIntents(context.Background(), poolcontroller.RequeuePayoutIntentsRequest{IDs: ids})
	if err != nil {
		return err
	}
	return writeJSON(stdout, struct {
		Status  string                        `json:"status"`
		Count   int                           `json:"count"`
		Intents []poolcontroller.PayoutIntent `json:"intents"`
	}{
		Status:  "requeued",
		Count:   len(requeued),
		Intents: requeued,
	})
}

func runListAlerts(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("list-alerts", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to pool-payout-executor config")
	status := fs.String("status", "", "optional payout intent status filter")
	roundID := fs.String("round-id", "", "optional round ID filter")
	memberEthAddress := fs.String("member-eth-address", "", "optional member address filter")
	limit := fs.Int("limit", 100, "optional max number of alerts")
	submittedOlderThanSeconds := fs.Int("submitted-older-than-seconds", 0, "override submitted stale threshold")
	failedOlderThanSeconds := fs.Int("failed-older-than-seconds", 0, "override failed stale threshold")
	leaseExpiresWithinSeconds := fs.Int("lease-expires-within-seconds", 0, "override lease-expiry warning threshold")
	retryCountAtLeast := fs.Int("retry-count-at-least", 0, "override retry-count alert threshold")
	recentRequeueWithinSeconds := fs.Int("recent-requeue-within-seconds", 0, "override recent requeue alert threshold")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfigPath(*configPath)
	if err != nil {
		return err
	}
	client, err := poolcontroller.NewClient(cfg.PoolController)
	if err != nil {
		return err
	}
	summary, alerts, err := client.ListPayoutAlerts(context.Background(), poolcontroller.ListPayoutAlertsOptions{
		RoundID:                    strings.TrimSpace(*roundID),
		MemberEthAddress:           strings.TrimSpace(*memberEthAddress),
		Status:                     strings.TrimSpace(*status),
		Limit:                      *limit,
		SubmittedOlderThanSeconds:  *submittedOlderThanSeconds,
		FailedOlderThanSeconds:     *failedOlderThanSeconds,
		LeaseExpiresWithinSeconds:  *leaseExpiresWithinSeconds,
		RetryCountAtLeast:          *retryCountAtLeast,
		RecentRequeueWithinSeconds: *recentRequeueWithinSeconds,
	})
	if err != nil {
		return err
	}
	return writeJSON(stdout, struct {
		Summary poolcontroller.PayoutAlertSummary `json:"summary"`
		Alerts  []poolcontroller.PayoutAlert      `json:"alerts"`
	}{
		Summary: summary,
		Alerts:  alerts,
	})
}

func runMarkStatus(args []string, stdout io.Writer, status string) error {
	fs := flag.NewFlagSet("mark-"+status, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to pool-payout-executor config")
	idsRaw := fs.String("ids", "", "comma-separated payout intent ids")
	externalRef := fs.String("external-ref", "", "optional batch or payout provider reference")
	txHash := fs.String("tx-hash", "", "optional on-chain transaction hash")
	reason := fs.String("reason", "", "required when status is failed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return errors.New("--config is required")
	}
	ids := parseCSVStrings(*idsRaw)
	if len(ids) == 0 {
		return errors.New("--ids is required")
	}
	if status == "failed" && strings.TrimSpace(*reason) == "" {
		return errors.New("--reason is required for failed status")
	}
	cfg, err := config.LoadFile(*configPath)
	if err != nil {
		return err
	}
	client, err := poolcontroller.NewClient(cfg.PoolController)
	if err != nil {
		return err
	}
	updated, err := client.UpdatePayoutIntentStatus(context.Background(), poolcontroller.UpdatePayoutIntentStatusRequest{
		IDs:           ids,
		Status:        status,
		ExternalRef:   strings.TrimSpace(*externalRef),
		TxHash:        strings.TrimSpace(*txHash),
		FailureReason: strings.TrimSpace(*reason),
	})
	if err != nil {
		return err
	}
	return writeJSON(stdout, struct {
		Status  string                        `json:"status"`
		Intents []poolcontroller.PayoutIntent `json:"intents"`
	}{
		Status:  status,
		Intents: updated,
	})
}

func runSendNativeBatch(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("send-native-batch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to pool-payout-executor config")
	status := fs.String("status", "", "optional payout intent status filter")
	roundID := fs.String("round-id", "", "optional round ID filter")
	memberEthAddress := fs.String("member-eth-address", "", "optional member address filter")
	limit := fs.Int("limit", 0, "optional max number of intents")
	dryRun := fs.Bool("dry-run", false, "preview batch without sending transactions or writing status")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfigPath(*configPath)
	if err != nil {
		return err
	}
	opts := poolcontroller.ListPayoutIntentsOptions{
		RoundID:          strings.TrimSpace(*roundID),
		MemberEthAddress: strings.TrimSpace(*memberEthAddress),
		Status:           strings.TrimSpace(*status),
		Limit:            *limit,
	}
	if opts.Status == "" {
		opts.Status = "exported"
	}
	if opts.Limit == 0 {
		opts.Limit = cfg.Executor.BatchSize
	}
	result, err := sendNativeBatch(context.Background(), cfg, nil, opts, *dryRun)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

func runReconcileOnce(args []string, stdout io.Writer) error {
	cfg, confirmOpts, failedOpts, sendOpts, dryRun, err := loadReconcileOptions(args, "reconcile-once")
	if err != nil {
		return err
	}
	stateRepo, err := openOptionalStateRepo(cfg)
	if err != nil {
		return err
	}
	if stateRepo != nil {
		defer stateRepo.Close()
	}
	result, err := reconcileOnce(context.Background(), cfg, stateRepo, confirmOpts, failedOpts, sendOpts, dryRun)
	if stateRepo != nil {
		if persistErr := persistReconcileState(stateRepo, result, err); persistErr != nil {
			return persistErr
		}
	}
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

func runReconcileLoop(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("reconcile-loop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to pool-payout-executor config")
	roundID := fs.String("round-id", "", "optional round ID filter")
	memberEthAddress := fs.String("member-eth-address", "", "optional member address filter")
	limit := fs.Int("limit", 0, "optional max number of intents for each phase")
	dryRun := fs.Bool("dry-run", false, "preview confirmation and send actions without writing status or broadcasting transactions")
	intervalMS := fs.Int("interval-ms", 5000, "loop interval in milliseconds")
	iterations := fs.Int("iterations", 0, "number of iterations to run before exiting; 0 means forever")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *intervalMS <= 0 {
		return errors.New("--interval-ms must be > 0")
	}
	cfg, err := loadConfigPath(*configPath)
	if err != nil {
		return err
	}
	confirmOpts := poolcontroller.ListPayoutIntentsOptions{
		RoundID:          strings.TrimSpace(*roundID),
		MemberEthAddress: strings.TrimSpace(*memberEthAddress),
		Status:           "submitted",
		Limit:            *limit,
	}
	failedOpts := poolcontroller.ListPayoutIntentsOptions{
		RoundID:          strings.TrimSpace(*roundID),
		MemberEthAddress: strings.TrimSpace(*memberEthAddress),
		Status:           "failed",
		Limit:            *limit,
	}
	sendOpts := poolcontroller.ListPayoutIntentsOptions{
		RoundID:          strings.TrimSpace(*roundID),
		MemberEthAddress: strings.TrimSpace(*memberEthAddress),
		Status:           "exported",
		Limit:            *limit,
	}
	if confirmOpts.Limit == 0 {
		confirmOpts.Limit = cfg.Executor.BatchSize
	}
	if failedOpts.Limit == 0 {
		failedOpts.Limit = cfg.Executor.BatchSize
	}
	if sendOpts.Limit == 0 {
		sendOpts.Limit = cfg.Executor.BatchSize
	}
	stateRepo, err := openOptionalStateRepo(cfg)
	if err != nil {
		return err
	}
	if stateRepo != nil {
		defer stateRepo.Close()
	}
	if addr := cfg.Executor.MetricsAddr; addr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", observability.NewMetricsHandler())
		srv := &http.Server{Addr: addr, Handler: mux}
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				_, _ = fmt.Fprintf(stdout, "{\"type\":\"metrics_listener\",\"status\":\"error\",\"error\":%q}\n", err.Error())
			}
		}()
		defer func() { _ = srv.Close() }()
	}
	results := make([]reconcileOnceResult, 0)
	runCount := 0
	for {
		iterStart := time.Now()
		result, err := reconcileOnce(context.Background(), cfg, stateRepo, confirmOpts, failedOpts, sendOpts, *dryRun)
		recordReconcileMetrics(result, err, time.Since(iterStart))
		if stateRepo != nil {
			if persistErr := persistReconcileState(stateRepo, result, err); persistErr != nil {
				return persistErr
			}
		}
		if err != nil {
			return err
		}
		results = append(results, result)
		runCount++
		if *iterations > 0 && runCount >= *iterations {
			break
		}
		time.Sleep(time.Duration(*intervalMS) * time.Millisecond)
	}
	return writeJSON(stdout, struct {
		DryRun     bool                  `json:"dry_run"`
		Iterations int                   `json:"iterations"`
		IntervalMS int                   `json:"interval_ms"`
		Executions []reconcileOnceResult `json:"executions"`
	}{
		DryRun:     *dryRun,
		Iterations: runCount,
		IntervalMS: *intervalMS,
		Executions: results,
	})
}

func runStateSummary(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("state-summary", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to pool-payout-executor config")
	runsLimit := fs.Int("runs-limit", 10, "max number of recent runs")
	intentsLimit := fs.Int("intents-limit", 25, "max number of persisted intent records")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfigPath(*configPath)
	if err != nil {
		return err
	}
	stateRepo, err := openOptionalStateRepo(cfg)
	if err != nil {
		return err
	}
	if stateRepo == nil {
		return errors.New("executor.state_path is required for state-summary")
	}
	defer stateRepo.Close()
	runs, err := stateRepo.ListRuns(*runsLimit)
	if err != nil {
		return err
	}
	intents, err := stateRepo.ListIntents(*intentsLimit)
	if err != nil {
		return err
	}
	return writeJSON(stdout, struct {
		StatePath string              `json:"state_path"`
		Runs      []repo.RunRecord    `json:"runs"`
		Intents   []repo.IntentRecord `json:"intents"`
	}{
		StatePath: cfg.Executor.StatePath,
		Runs:      runs,
		Intents:   intents,
	})
}

func loadReconcileOptions(args []string, command string) (*config.Config, poolcontroller.ListPayoutIntentsOptions, poolcontroller.ListPayoutIntentsOptions, poolcontroller.ListPayoutIntentsOptions, bool, error) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to pool-payout-executor config")
	roundID := fs.String("round-id", "", "optional round ID filter")
	memberEthAddress := fs.String("member-eth-address", "", "optional member address filter")
	limit := fs.Int("limit", 0, "optional max number of intents for each phase")
	dryRun := fs.Bool("dry-run", false, "preview confirmation and send actions without writing status or broadcasting transactions")
	if err := fs.Parse(args); err != nil {
		return nil, poolcontroller.ListPayoutIntentsOptions{}, poolcontroller.ListPayoutIntentsOptions{}, poolcontroller.ListPayoutIntentsOptions{}, false, err
	}
	cfg, err := loadConfigPath(*configPath)
	if err != nil {
		return nil, poolcontroller.ListPayoutIntentsOptions{}, poolcontroller.ListPayoutIntentsOptions{}, poolcontroller.ListPayoutIntentsOptions{}, false, err
	}
	confirmOpts := poolcontroller.ListPayoutIntentsOptions{
		RoundID:          strings.TrimSpace(*roundID),
		MemberEthAddress: strings.TrimSpace(*memberEthAddress),
		Status:           "submitted",
		Limit:            *limit,
	}
	failedOpts := poolcontroller.ListPayoutIntentsOptions{
		RoundID:          strings.TrimSpace(*roundID),
		MemberEthAddress: strings.TrimSpace(*memberEthAddress),
		Status:           "failed",
		Limit:            *limit,
	}
	sendOpts := poolcontroller.ListPayoutIntentsOptions{
		RoundID:          strings.TrimSpace(*roundID),
		MemberEthAddress: strings.TrimSpace(*memberEthAddress),
		Status:           "exported",
		Limit:            *limit,
	}
	if confirmOpts.Limit == 0 {
		confirmOpts.Limit = cfg.Executor.BatchSize
	}
	if failedOpts.Limit == 0 {
		failedOpts.Limit = cfg.Executor.BatchSize
	}
	if sendOpts.Limit == 0 {
		sendOpts.Limit = cfg.Executor.BatchSize
	}
	return cfg, confirmOpts, failedOpts, sendOpts, *dryRun, nil
}

type reconcileOnceResult struct {
	DryRun      bool                   `json:"dry_run"`
	StartedAt   time.Time              `json:"started_at"`
	CompletedAt time.Time              `json:"completed_at,omitempty"`
	Confirm     confirmSubmittedResult `json:"confirm"`
	AutoRequeue autoRequeueResult      `json:"auto_requeue"`
	Dispatch    sendNativeBatchResult  `json:"dispatch"`
}

// recordReconcileMetrics writes per-iteration counters and the
// per-action outcome tallies from a reconcileOnceResult into the
// observability collectors. Best-effort: a nil result or zero-length
// actions slice is a safe no-op.
func recordReconcileMetrics(result reconcileOnceResult, runErr error, dur time.Duration) {
	outcome := "success"
	if runErr != nil {
		outcome = "error"
	}
	observability.RecordReconcileIteration(outcome, dur.Seconds())
	for _, action := range result.Confirm.Actions {
		observability.RecordTransactionConfirmed(action.Status)
	}
	for _, action := range result.Dispatch.Actions {
		observability.RecordTransactionSubmitted(action.Status)
	}
}

func reconcileOnce(ctx context.Context, cfg *config.Config, stateRepo *repo.StateRepo, confirmOpts, failedOpts, sendOpts poolcontroller.ListPayoutIntentsOptions, dryRun bool) (reconcileOnceResult, error) {
	result := reconcileOnceResult{
		DryRun:    dryRun,
		StartedAt: time.Now().UTC(),
	}
	confirmResult, err := confirmSubmitted(ctx, cfg, stateRepo, confirmOpts, dryRun)
	if err != nil {
		result.CompletedAt = time.Now().UTC()
		return result, err
	}
	result.Confirm = confirmResult
	requeueResult, err := autoRequeueFailed(ctx, cfg, failedOpts, dryRun)
	if err != nil {
		result.CompletedAt = time.Now().UTC()
		return result, err
	}
	result.AutoRequeue = requeueResult
	sendResult, err := sendNativeBatch(ctx, cfg, stateRepo, sendOpts, dryRun)
	if err != nil {
		result.CompletedAt = time.Now().UTC()
		return result, err
	}
	result.Dispatch = sendResult
	result.CompletedAt = time.Now().UTC()
	return result, nil
}

type sendNativeBatchResult struct {
	DryRun   bool                `json:"dry_run,omitempty"`
	From     string              `json:"from,omitempty"`
	LeaseID  string              `json:"lease_id,omitempty"`
	TotalWei string              `json:"total_wei"`
	Results  []map[string]any    `json:"results"`
	Actions  []repo.IntentUpdate `json:"-"`
}

type autoRequeueResult struct {
	DryRun  bool                `json:"dry_run,omitempty"`
	Enabled bool                `json:"enabled"`
	Results []map[string]any    `json:"results"`
	Actions []repo.IntentUpdate `json:"-"`
}

func autoRequeueFailed(ctx context.Context, cfg *config.Config, opts poolcontroller.ListPayoutIntentsOptions, dryRun bool) (autoRequeueResult, error) {
	result := autoRequeueResult{
		DryRun:  dryRun,
		Enabled: cfg.Executor.AutoRequeueFailed,
		Results: make([]map[string]any, 0),
		Actions: make([]repo.IntentUpdate, 0),
	}
	if !cfg.Executor.AutoRequeueFailed {
		return result, nil
	}
	controllerClient, err := poolcontroller.NewClient(cfg.PoolController)
	if err != nil {
		return autoRequeueResult{}, err
	}
	intents, err := controllerClient.ListPayoutIntents(ctx, opts)
	if err != nil {
		return autoRequeueResult{}, err
	}
	eligible := make([]poolcontroller.PayoutIntent, 0, len(intents))
	for _, intent := range intents {
		status, reason := autoRequeueDecision(intent, cfg.Executor, time.Now().UTC())
		entry := map[string]any{"id": intent.ID, "status": status}
		if reason != "" {
			entry["reason"] = reason
		}
		result.Results = append(result.Results, entry)
		if status == "eligible" {
			eligible = append(eligible, intent)
		}
	}
	if len(eligible) == 0 {
		return result, nil
	}
	if dryRun {
		for _, intent := range eligible {
			result.Actions = append(result.Actions, repo.IntentUpdate{IntentID: intent.ID, Phase: "requeue", Status: "would_requeue", Succeeded: true})
		}
		for _, entry := range result.Results {
			if entry["status"] == "eligible" {
				entry["status"] = "would_requeue"
			}
		}
		return result, nil
	}
	ids := make([]string, 0, len(eligible))
	for _, intent := range eligible {
		ids = append(ids, intent.ID)
	}
	requeued, err := controllerClient.RequeuePayoutIntents(ctx, poolcontroller.RequeuePayoutIntentsRequest{IDs: ids})
	if err != nil {
		return autoRequeueResult{}, err
	}
	for _, intent := range requeued {
		result.Actions = append(result.Actions, repo.IntentUpdate{IntentID: intent.ID, Phase: "requeue", Status: "requeued", Succeeded: true})
	}
	requeuedSet := make(map[string]struct{}, len(requeued))
	for _, intent := range requeued {
		requeuedSet[intent.ID] = struct{}{}
	}
	for _, entry := range result.Results {
		if id, ok := entry["id"].(string); ok {
			if _, found := requeuedSet[id]; found {
				entry["status"] = "requeued"
			}
		}
	}
	return result, nil
}

func autoRequeueDecision(intent poolcontroller.PayoutIntent, execCfg config.Executor, now time.Time) (string, string) {
	if intent.Status != "failed" {
		return "skipped", "status is not failed"
	}
	if execCfg.MaxRetries > 0 && int(intent.RetryCount) >= execCfg.MaxRetries {
		return "skipped", "retry limit reached"
	}
	failedAt := parseRFC3339Time(intent.FailedAt)
	if failedAt.IsZero() {
		return "skipped", "missing failed_at"
	}
	cooldown := time.Duration(execCfg.RequeueCooldownSeconds) * time.Second
	if cooldown > 0 && now.Sub(failedAt) < cooldown {
		return "skipped", "cooldown not elapsed"
	}
	if !isTransientFailure(intent.FailureReason) {
		return "skipped", "failure is not transient"
	}
	return "eligible", ""
}

func parseRFC3339Time(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func isTransientFailure(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason == "" {
		return false
	}
	permanentMarkers := []string{
		"insufficient",
		"revert",
		"unauthorized",
		"forbidden",
		"invalid",
		"unsupported",
		"manual",
		"compliance",
	}
	for _, marker := range permanentMarkers {
		if strings.Contains(reason, marker) {
			return false
		}
	}
	transientMarkers := []string{
		"timeout",
		"tempor",
		"unavailable",
		"connection",
		"reset",
		"deadline exceeded",
		"eof",
		"429",
		"502",
		"503",
		"504",
		"pending",
		"receipt not available",
		"not found",
	}
	for _, marker := range transientMarkers {
		if strings.Contains(reason, marker) {
			return true
		}
	}
	return false
}

func sendNativeBatch(ctx context.Context, cfg *config.Config, stateRepo *repo.StateRepo, opts poolcontroller.ListPayoutIntentsOptions, dryRun bool) (sendNativeBatchResult, error) {
	controllerClient, err := poolcontroller.NewClient(cfg.PoolController)
	if err != nil {
		return sendNativeBatchResult{}, err
	}
	leaseID, intents, err := loadDispatchIntents(ctx, controllerClient, cfg.Executor, opts, dryRun)
	if err != nil {
		return sendNativeBatchResult{}, err
	}
	total := big.NewInt(0)
	results := make([]map[string]any, 0, len(intents))
	actions := make([]repo.IntentUpdate, 0, len(intents))
	eligible := make([]poolcontroller.PayoutIntent, 0, len(intents))
	untouchedLeaseIDs := make([]string, 0, len(intents))
	for _, intent := range intents {
		if retryAfterMS, backedOff, err := retryAfterForIntent(stateRepo, cfg.Executor, "dispatch", intent.ID, time.Now().UTC()); err != nil {
			return sendNativeBatchResult{}, err
		} else if backedOff {
			results = append(results, map[string]any{
				"id":             intent.ID,
				"status":         "backoff_skipped",
				"retry_after_ms": retryAfterMS,
			})
			actions = append(actions, repo.IntentUpdate{IntentID: intent.ID, Phase: "dispatch", Status: "backoff_skipped"})
			untouchedLeaseIDs = append(untouchedLeaseIDs, intent.ID)
			continue
		}
		if err := validateNativeIntent(intent, cfg.Executor.ChainID); err != nil {
			results = append(results, map[string]any{
				"id":     intent.ID,
				"status": "skipped",
				"error":  err.Error(),
			})
			actions = append(actions, repo.IntentUpdate{IntentID: intent.ID, Phase: "dispatch", Status: "skipped", Error: err.Error()})
			untouchedLeaseIDs = append(untouchedLeaseIDs, intent.ID)
			continue
		}
		if strings.TrimSpace(intent.TxHash) != "" || strings.TrimSpace(intent.ExternalRef) != "" {
			results = append(results, map[string]any{
				"id":     intent.ID,
				"status": "skipped",
				"error":  "intent already has settlement metadata",
			})
			actions = append(actions, repo.IntentUpdate{IntentID: intent.ID, Phase: "dispatch", Status: "skipped", Error: "intent already has settlement metadata"})
			untouchedLeaseIDs = append(untouchedLeaseIDs, intent.ID)
			continue
		}
		amount, _ := new(big.Int).SetString(intent.AmountWei, 10)
		total.Add(total, amount)
		eligible = append(eligible, intent)
	}
	if dryRun {
		for _, intent := range eligible {
			results = append(results, map[string]any{
				"id":                  intent.ID,
				"status":              "dry_run",
				"destination_address": intent.DestinationAddress,
				"amount_wei":          intent.AmountWei,
			})
			actions = append(actions, repo.IntentUpdate{IntentID: intent.ID, Phase: "dispatch", Status: "dry_run"})
		}
		return sendNativeBatchResult{
			DryRun:   true,
			LeaseID:  leaseID,
			TotalWei: total.String(),
			Results:  results,
			Actions:  actions,
		}, nil
	}
	if len(eligible) == 0 {
		if err := releaseLeaseIfHeld(ctx, controllerClient, cfg.Executor, leaseID); err != nil {
			return sendNativeBatchResult{}, err
		}
		return sendNativeBatchResult{
			LeaseID:  leaseID,
			TotalWei: total.String(),
			Results:  results,
			Actions:  actions,
		}, nil
	}
	chainClient, err := ethclientx.New(ctx, cfg.Executor, ethclientx.Options{Metrics: observability.ChainMetrics()})
	if err != nil {
		if releaseErr := releaseLeaseIfHeld(ctx, controllerClient, cfg.Executor, leaseID); releaseErr != nil {
			return sendNativeBatchResult{}, releaseErr
		}
		return sendNativeBatchResult{}, err
	}
	defer chainClient.Close()
	balance, err := chainClient.BalanceAt(ctx)
	if err != nil {
		return sendNativeBatchResult{}, err
	}
	if balance.Cmp(total) < 0 {
		if releaseErr := releaseLeaseIfHeld(ctx, controllerClient, cfg.Executor, leaseID); releaseErr != nil {
			return sendNativeBatchResult{}, releaseErr
		}
		return sendNativeBatchResult{}, fmt.Errorf("insufficient wallet balance %s for payout total %s", balance.String(), total.String())
	}
	submittedCount := 0
	for _, intent := range eligible {
		amount, _ := new(big.Int).SetString(intent.AmountWei, 10)
		sent, err := chainClient.SendNativeTransfer(ctx, ethcommon.HexToAddress(intent.DestinationAddress), amount)
		if err != nil {
			results = append(results, map[string]any{
				"id":     intent.ID,
				"status": "error",
				"error":  err.Error(),
			})
			actions = append(actions, repo.IntentUpdate{
				IntentID:        intent.ID,
				Phase:           "dispatch",
				Status:          "error",
				Error:           err.Error(),
				DispatchAttempt: true,
				Failed:          true,
			})
			continue
		}
		updated, err := controllerClient.UpdatePayoutIntentStatus(context.Background(), poolcontroller.UpdatePayoutIntentStatusRequest{
			IDs:         []string{intent.ID},
			Status:      "submitted",
			LeaseID:     leaseID,
			ExternalRef: fmt.Sprintf("nonce-%d", sent.Nonce),
			TxHash:      sent.TxHash,
		})
		if err != nil {
			return sendNativeBatchResult{}, err
		}
		results = append(results, map[string]any{
			"id":      intent.ID,
			"status":  "submitted",
			"tx_hash": sent.TxHash,
			"intent":  updated[0],
		})
		submittedCount++
		actions = append(actions, repo.IntentUpdate{
			IntentID:        intent.ID,
			Phase:           "dispatch",
			Status:          "submitted",
			TxHash:          sent.TxHash,
			DispatchAttempt: true,
			Succeeded:       true,
		})
	}
	if submittedCount > 0 && len(untouchedLeaseIDs) > 0 {
		if err := releaseLeaseIDs(ctx, controllerClient, cfg.Executor, leaseID, untouchedLeaseIDs); err != nil {
			return sendNativeBatchResult{}, err
		}
	}
	return sendNativeBatchResult{
		From:     chainClient.FromAddress().Hex(),
		LeaseID:  leaseID,
		TotalWei: total.String(),
		Results:  results,
		Actions:  actions,
	}, nil
}

func loadDispatchIntents(ctx context.Context, controllerClient *poolcontroller.Client, execCfg config.Executor, opts poolcontroller.ListPayoutIntentsOptions, dryRun bool) (string, []poolcontroller.PayoutIntent, error) {
	if dryRun || opts.Status != "exported" {
		intents, err := controllerClient.ListPayoutIntents(ctx, opts)
		if err != nil {
			return "", nil, err
		}
		return "", intents, nil
	}
	leased, err := controllerClient.ListPayoutIntents(ctx, poolcontroller.ListPayoutIntentsOptions{
		RoundID:          opts.RoundID,
		MemberEthAddress: opts.MemberEthAddress,
		Status:           "leased",
		Limit:            opts.Limit,
	})
	if err != nil {
		return "", nil, err
	}
	owned := make([]poolcontroller.PayoutIntent, 0, len(leased))
	leaseID := ""
	for _, intent := range leased {
		if strings.TrimSpace(intent.LeaseOwner) != execCfg.ExecutorID {
			continue
		}
		if leaseID == "" {
			leaseID = strings.TrimSpace(intent.LeaseID)
		}
		if strings.TrimSpace(intent.LeaseID) != leaseID {
			continue
		}
		owned = append(owned, intent)
	}
	if len(owned) > 0 && leaseID != "" {
		renewedLeaseID, renewed, err := controllerClient.RenewPayoutIntents(ctx, poolcontroller.RenewPayoutIntentsRequest{
			ExecutorID:      execCfg.ExecutorID,
			LeaseID:         leaseID,
			LeaseTTLSeconds: execCfg.LeaseTTLSeconds,
		})
		if err != nil {
			return "", nil, err
		}
		return renewedLeaseID, renewed, nil
	}
	leaseID, claimed, err := controllerClient.ClaimPayoutIntents(ctx, poolcontroller.ClaimPayoutIntentsRequest{
		ExecutorID:       execCfg.ExecutorID,
		LeaseTTLSeconds:  execCfg.LeaseTTLSeconds,
		RoundID:          opts.RoundID,
		MemberEthAddress: opts.MemberEthAddress,
		Limit:            opts.Limit,
	})
	if err != nil {
		return "", nil, err
	}
	return leaseID, claimed, nil
}

func releaseLeaseIfHeld(ctx context.Context, controllerClient *poolcontroller.Client, execCfg config.Executor, leaseID string) error {
	if strings.TrimSpace(leaseID) == "" {
		return nil
	}
	_, _, err := controllerClient.ReleasePayoutIntents(ctx, poolcontroller.ReleasePayoutIntentsRequest{
		ExecutorID: execCfg.ExecutorID,
		LeaseID:    leaseID,
	})
	return err
}

func releaseLeaseIDs(ctx context.Context, controllerClient *poolcontroller.Client, execCfg config.Executor, leaseID string, ids []string) error {
	if strings.TrimSpace(leaseID) == "" || len(ids) == 0 {
		return nil
	}
	_, _, err := controllerClient.ReleasePayoutIntents(ctx, poolcontroller.ReleasePayoutIntentsRequest{
		ExecutorID: execCfg.ExecutorID,
		LeaseID:    leaseID,
		IDs:        ids,
	})
	return err
}

func runConfirmSubmitted(args []string, stdout io.Writer) error {
	cfg, opts, err := loadListOptions(args, "confirm-submitted")
	if err != nil {
		return err
	}
	if opts.Status == "" {
		opts.Status = "submitted"
	}
	if opts.Limit == 0 {
		opts.Limit = cfg.Executor.BatchSize
	}
	result, err := confirmSubmitted(context.Background(), cfg, nil, opts, false)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

type confirmSubmittedResult struct {
	DryRun  bool                `json:"dry_run,omitempty"`
	Results []map[string]any    `json:"results"`
	Actions []repo.IntentUpdate `json:"-"`
}

func confirmSubmitted(ctx context.Context, cfg *config.Config, stateRepo *repo.StateRepo, opts poolcontroller.ListPayoutIntentsOptions, dryRun bool) (confirmSubmittedResult, error) {
	controllerClient, err := poolcontroller.NewClient(cfg.PoolController)
	if err != nil {
		return confirmSubmittedResult{}, err
	}
	intents, err := controllerClient.ListPayoutIntents(ctx, opts)
	if err != nil {
		return confirmSubmittedResult{}, err
	}
	chainClient, err := ethclientx.New(ctx, cfg.Executor, ethclientx.Options{Metrics: observability.ChainMetrics()})
	if err != nil {
		return confirmSubmittedResult{}, err
	}
	defer chainClient.Close()
	results := make([]map[string]any, 0, len(intents))
	actions := make([]repo.IntentUpdate, 0, len(intents))
	for _, intent := range intents {
		if retryAfterMS, backedOff, err := retryAfterForIntent(stateRepo, cfg.Executor, "confirm", intent.ID, time.Now().UTC()); err != nil {
			return confirmSubmittedResult{}, err
		} else if backedOff {
			results = append(results, map[string]any{"id": intent.ID, "status": "backoff_skipped", "retry_after_ms": retryAfterMS})
			actions = append(actions, repo.IntentUpdate{IntentID: intent.ID, Phase: "confirm", Status: "backoff_skipped"})
			continue
		}
		if err := validateNativeIntent(intent, cfg.Executor.ChainID); err != nil {
			results = append(results, map[string]any{"id": intent.ID, "status": "skipped", "error": err.Error()})
			actions = append(actions, repo.IntentUpdate{IntentID: intent.ID, Phase: "confirm", Status: "skipped", Error: err.Error()})
			continue
		}
		if strings.TrimSpace(intent.TxHash) == "" {
			results = append(results, map[string]any{"id": intent.ID, "status": "skipped", "error": "missing tx_hash"})
			actions = append(actions, repo.IntentUpdate{IntentID: intent.ID, Phase: "confirm", Status: "skipped", Error: "missing tx_hash"})
			continue
		}
		confirmed, err := chainClient.ConfirmTransaction(ctx, intent.TxHash, cfg.Executor.ConfirmationBlocks)
		if err != nil {
			if dryRun {
				results = append(results, map[string]any{"id": intent.ID, "status": "would_mark_failed", "tx_hash": intent.TxHash, "error": err.Error()})
				actions = append(actions, repo.IntentUpdate{IntentID: intent.ID, Phase: "confirm", Status: "would_mark_failed", Error: err.Error(), TxHash: intent.TxHash, ConfirmCheck: true, Failed: true})
				continue
			}
			updated, updateErr := controllerClient.UpdatePayoutIntentStatus(ctx, poolcontroller.UpdatePayoutIntentStatusRequest{
				IDs:           []string{intent.ID},
				Status:        "failed",
				ExternalRef:   intent.ExternalRef,
				TxHash:        intent.TxHash,
				FailureReason: err.Error(),
			})
			if updateErr != nil {
				return confirmSubmittedResult{}, updateErr
			}
			results = append(results, map[string]any{"id": intent.ID, "status": "failed", "intent": updated[0]})
			actions = append(actions, repo.IntentUpdate{IntentID: intent.ID, Phase: "confirm", Status: "failed", Error: err.Error(), TxHash: intent.TxHash, ConfirmCheck: true, Failed: true})
			continue
		}
		if !confirmed {
			results = append(results, map[string]any{"id": intent.ID, "status": "pending_confirmation", "tx_hash": intent.TxHash})
			actions = append(actions, repo.IntentUpdate{IntentID: intent.ID, Phase: "confirm", Status: "pending_confirmation", TxHash: intent.TxHash, ConfirmCheck: true})
			continue
		}
		if dryRun {
			results = append(results, map[string]any{"id": intent.ID, "status": "would_mark_paid", "tx_hash": intent.TxHash})
			actions = append(actions, repo.IntentUpdate{IntentID: intent.ID, Phase: "confirm", Status: "would_mark_paid", TxHash: intent.TxHash, ConfirmCheck: true, Succeeded: true})
			continue
		}
		updated, err := controllerClient.UpdatePayoutIntentStatus(ctx, poolcontroller.UpdatePayoutIntentStatusRequest{
			IDs:         []string{intent.ID},
			Status:      "paid",
			ExternalRef: intent.ExternalRef,
			TxHash:      intent.TxHash,
		})
		if err != nil {
			return confirmSubmittedResult{}, err
		}
		results = append(results, map[string]any{"id": intent.ID, "status": "paid", "intent": updated[0]})
		actions = append(actions, repo.IntentUpdate{IntentID: intent.ID, Phase: "confirm", Status: "paid", TxHash: intent.TxHash, ConfirmCheck: true, Succeeded: true})
	}
	return confirmSubmittedResult{
		DryRun:  dryRun,
		Results: results,
		Actions: actions,
	}, nil
}

func openOptionalStateRepo(cfg *config.Config) (*repo.StateRepo, error) {
	if strings.TrimSpace(cfg.Executor.StatePath) == "" {
		return nil, nil
	}
	return repo.Open(strings.TrimSpace(cfg.Executor.StatePath), cfg.Executor.RunHistoryLimit)
}

func retryAfterForIntent(stateRepo *repo.StateRepo, execCfg config.Executor, phase, intentID string, now time.Time) (int, bool, error) {
	if stateRepo == nil || execCfg.BackoffBaseMS <= 0 || execCfg.BackoffMaxMS <= 0 {
		return 0, false, nil
	}
	rec, found, err := stateRepo.GetIntent(intentID)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}
	nextEligibleAt, ok := nextEligibleTime(execCfg, phase, rec)
	if !ok || !nextEligibleAt.After(now) {
		return 0, false, nil
	}
	return int(nextEligibleAt.Sub(now).Milliseconds()), true, nil
}

func nextEligibleTime(execCfg config.Executor, phase string, rec repo.IntentRecord) (time.Time, bool) {
	switch phase {
	case "confirm":
		switch rec.LastStatus {
		case "pending_confirmation":
			return rec.LastSeenAt.Add(backoffDuration(execCfg, int(rec.ConfirmChecks))), true
		case "failed", "would_mark_failed":
			return rec.LastSeenAt.Add(backoffDuration(execCfg, int(rec.FailureCount))), true
		}
	case "dispatch":
		switch rec.LastStatus {
		case "error", "failed", "would_mark_failed":
			return rec.LastSeenAt.Add(backoffDuration(execCfg, int(rec.FailureCount))), true
		}
	}
	return time.Time{}, false
}

func backoffDuration(execCfg config.Executor, attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delayMS := execCfg.BackoffBaseMS
	maxMS := execCfg.BackoffMaxMS
	for i := 1; i < attempts; i++ {
		if delayMS >= maxMS {
			delayMS = maxMS
			break
		}
		delayMS *= 2
		if delayMS > maxMS {
			delayMS = maxMS
			break
		}
	}
	return time.Duration(delayMS) * time.Millisecond
}

func persistReconcileState(stateRepo *repo.StateRepo, result reconcileOnceResult, runErr error) error {
	if stateRepo == nil {
		return nil
	}
	runRecord := repo.RunRecord{
		RunID:                result.CompletedAt.Format(time.RFC3339Nano),
		StartedAt:            result.StartedAt,
		CompletedAt:          result.CompletedAt,
		DryRun:               result.DryRun,
		ConfirmStatusCounts:  summarizeActions(result.Confirm.Actions),
		RequeueStatusCounts:  summarizeActions(result.AutoRequeue.Actions),
		DispatchStatusCounts: summarizeActions(result.Dispatch.Actions),
	}
	if runErr != nil {
		runRecord.Error = runErr.Error()
	}
	if err := stateRepo.SaveRun(runRecord); err != nil {
		return err
	}
	for _, action := range result.Confirm.Actions {
		if action.Status == "backoff_skipped" {
			continue
		}
		if err := stateRepo.UpsertIntent(action, result.CompletedAt); err != nil {
			return err
		}
	}
	for _, action := range result.AutoRequeue.Actions {
		if action.Status == "backoff_skipped" {
			continue
		}
		if err := stateRepo.UpsertIntent(action, result.CompletedAt); err != nil {
			return err
		}
	}
	for _, action := range result.Dispatch.Actions {
		if action.Status == "backoff_skipped" {
			continue
		}
		if err := stateRepo.UpsertIntent(action, result.CompletedAt); err != nil {
			return err
		}
	}
	return nil
}

func summarizeActions(actions []repo.IntentUpdate) map[string]uint64 {
	if len(actions) == 0 {
		return nil
	}
	out := make(map[string]uint64)
	for _, action := range actions {
		out[action.Status]++
	}
	return out
}

func loadConfigPath(configPath string) (*config.Config, error) {
	if strings.TrimSpace(configPath) == "" {
		return nil, errors.New("--config is required")
	}
	return config.LoadFile(strings.TrimSpace(configPath))
}

func loadListOptions(args []string, command string) (*config.Config, poolcontroller.ListPayoutIntentsOptions, error) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to pool-payout-executor config")
	status := fs.String("status", "", "optional payout intent status filter")
	roundID := fs.String("round-id", "", "optional round ID filter")
	memberEthAddress := fs.String("member-eth-address", "", "optional member address filter")
	limit := fs.Int("limit", 0, "optional max number of intents")
	if err := fs.Parse(args); err != nil {
		return nil, poolcontroller.ListPayoutIntentsOptions{}, err
	}
	if *configPath == "" {
		return nil, poolcontroller.ListPayoutIntentsOptions{}, errors.New("--config is required")
	}
	cfg, err := loadConfigPath(*configPath)
	if err != nil {
		return nil, poolcontroller.ListPayoutIntentsOptions{}, err
	}
	return cfg, poolcontroller.ListPayoutIntentsOptions{
		RoundID:          strings.TrimSpace(*roundID),
		MemberEthAddress: strings.TrimSpace(*memberEthAddress),
		Status:           strings.TrimSpace(*status),
		Limit:            *limit,
	}, nil
}

func parseCSVStrings(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func addDecimalStrings(a, b string) string {
	ai, ok := new(big.Int).SetString(a, 10)
	if !ok {
		return a
	}
	bi, ok := new(big.Int).SetString(b, 10)
	if !ok {
		return a
	}
	return new(big.Int).Add(ai, bi).String()
}

func validateNativeIntent(intent poolcontroller.PayoutIntent, chainID uint64) error {
	if intent.Asset != "native_eth" {
		return fmt.Errorf("unsupported asset %q", intent.Asset)
	}
	if intent.ChainID != chainID {
		return fmt.Errorf("intent chain_id %d does not match configured chain_id %d", intent.ChainID, chainID)
	}
	if !strings.HasPrefix(strings.ToLower(intent.DestinationAddress), "0x") || len(intent.DestinationAddress) != 42 {
		return fmt.Errorf("invalid destination address %q", intent.DestinationAddress)
	}
	if _, ok := new(big.Int).SetString(intent.AmountWei, 10); !ok {
		return fmt.Errorf("invalid amount_wei %q", intent.AmountWei)
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func usageError(w io.Writer) error {
	_, _ = fmt.Fprintln(w, "usage: livepeer-pool-payout-executor <list-intents|prepare-batch|send-native-batch|confirm-submitted|list-alerts|requeue-failed|requeue-alerted-failed|reconcile-once|reconcile-loop|state-summary|mark-submitted|mark-paid|mark-failed|version> [flags]")
	return errors.New("invalid command")
}
