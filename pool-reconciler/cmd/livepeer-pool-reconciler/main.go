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
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/paymentdaemon"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/poolcontroller"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/protocoldaemon"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/types"
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
	case "close-round":
		return runCloseRound(args[1:], stdout)
	case "watch-rounds":
		return runWatchRounds(args[1:], stdout)
	case "prepare-round-close":
		return runPrepareRoundClose(args[1:], stdout)
	case "get-round-revenue":
		return runGetRoundRevenue(args[1:], stdout)
	case "submit-round-close":
		return runSubmitRoundClose(args[1:], stdout)
	case "get-round-status":
		return runGetRoundStatus(args[1:], stdout)
	case "stream-round-events":
		return runStreamRoundEvents(args[1:], stdout)
	case "version":
		_, err := fmt.Fprintln(stdout, version)
		return err
	default:
		return usageError(stderr)
	}
}

func runSubmitRoundClose(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("submit-round-close", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to pool-reconciler config")
	requestPath := fs.String("request", "", "path to round-close request JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return errors.New("--config is required")
	}
	if *requestPath == "" {
		return errors.New("--request is required")
	}

	cfg, err := config.LoadFile(*configPath)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*requestPath)
	if err != nil {
		return fmt.Errorf("read request: %w", err)
	}
	var req types.RoundCloseRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("parse request JSON: %w", err)
	}
	if err := validateRoundCloseRequest(req); err != nil {
		return err
	}
	if err := validateRoundCloseAgainstRoundSource(context.Background(), cfg, req); err != nil {
		return err
	}

	client, err := poolcontroller.NewClient(cfg.PoolController)
	if err != nil {
		return err
	}
	if err := client.SubmitRoundClose(context.Background(), req); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "submitted round close %s for round %s\n", req.ID, req.RoundID)
	return err
}

func runPrepareRoundClose(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("prepare-round-close", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to pool-reconciler config")
	outputPath := fs.String("output", "", "optional output file path for generated round-close JSON")
	roundIDFlag := fs.Uint64("round-id", 0, "optional explicit round ID to prepare instead of current_round-1")
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
	req, err := prepareRoundCloseRequest(context.Background(), cfg, *roundIDFlag)
	if err != nil {
		return err
	}
	if *outputPath != "" {
		return writeJSONFile(*outputPath, req)
	}
	return writeJSON(stdout, req)
}

func runCloseRound(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("close-round", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to pool-reconciler config")
	roundIDFlag := fs.Uint64("round-id", 0, "optional explicit round ID to close instead of current_round-1")
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
	client, err := poolcontroller.NewClient(cfg.PoolController)
	if err != nil {
		return err
	}
	stateRepo, err := openStateRepo(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = stateRepo.Close() }()
	result, err := attemptRoundClose(context.Background(), cfg, client, stateRepo, *roundIDFlag)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "closed round %v with %v work receipts\n", result["closed_round"], result["work_receipt_count"])
	return err
}

func runWatchRounds(args []string, stdout io.Writer) error {
	cfg, err := loadConfigForRoundSource(args, "watch-rounds")
	if err != nil {
		return err
	}
	protocolClient, err := newProtocolDaemonClient(cfg)
	if err != nil {
		return err
	}
	controllerClient, err := poolcontroller.NewClient(cfg.PoolController)
	if err != nil {
		return err
	}
	stateRepo, err := openStateRepo(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = stateRepo.Close() }()
	enc := json.NewEncoder(stdout)
	var opMu sync.Mutex
	if err := backfillClosedRounds(context.Background(), cfg, protocolClient, controllerClient, stateRepo, enc, &opMu); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	streamErrCh := make(chan error, 1)
	go func() {
		streamErrCh <- protocolClient.StreamRoundEvents(ctx, func(evt protocoldaemon.RoundEvent) error {
			opMu.Lock()
			defer opMu.Unlock()
			if evt.Number <= 1 {
				return enc.Encode(map[string]any{
					"type":         "round_event",
					"round_id":     evt.Number,
					"closed_round": 0,
					"status":       "skipped",
					"reason":       "no completed prior round",
				})
			}
			record, found, err := stateRepo.GetRound(evt.Number - 1)
			if err != nil {
				return err
			}
			if found && record.Status == "closed" {
				return enc.Encode(map[string]any{
					"type":         "round_event",
					"round_id":     evt.Number,
					"closed_round": evt.Number - 1,
					"status":       "skipped",
					"reason":       "already closed",
				})
			}
			result, err := attemptRoundClose(ctx, cfg, controllerClient, stateRepo, evt.Number-1)
			if err != nil {
				return enc.Encode(map[string]any{
					"type":         "round_event",
					"round_id":     evt.Number,
					"closed_round": evt.Number - 1,
					"status":       "error",
					"error":        err.Error(),
				})
			}
			result["type"] = "round_event"
			result["round_id"] = evt.Number
			return enc.Encode(result)
		})
	}()
	if cfg.Reconcile.RetryInterval <= 0 {
		return <-streamErrCh
	}
	ticker := time.NewTicker(time.Duration(cfg.Reconcile.RetryInterval) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-streamErrCh:
			return err
		case <-ticker.C:
			opMu.Lock()
			err := retryPendingRounds(ctx, cfg, controllerClient, stateRepo, enc)
			opMu.Unlock()
			if err != nil {
				cancel()
				if streamErr := <-streamErrCh; streamErr != nil && !errors.Is(streamErr, context.Canceled) {
					return streamErr
				}
				return err
			}
		}
	}
}

func runGetRoundStatus(args []string, stdout io.Writer) error {
	cfg, err := loadConfigForRoundSource(args, "get-round-status")
	if err != nil {
		return err
	}
	client, err := newProtocolDaemonClient(cfg)
	if err != nil {
		return err
	}
	status, err := client.GetRoundStatus(context.Background())
	if err != nil {
		return err
	}
	return writeJSON(stdout, status)
}

func runGetRoundRevenue(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("get-round-revenue", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to pool-reconciler config")
	roundID := fs.Int64("round-id", -1, "round ID to query")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return errors.New("--config is required")
	}
	if *roundID < 0 {
		return errors.New("--round-id is required")
	}
	cfg, err := config.LoadFile(*configPath)
	if err != nil {
		return err
	}
	client, err := newPaymentDaemonClient(cfg)
	if err != nil {
		return err
	}
	revenue, err := client.GetRoundRevenue(context.Background(), *roundID)
	if err != nil {
		return err
	}
	return writeJSON(stdout, revenue)
}

func runStreamRoundEvents(args []string, stdout io.Writer) error {
	cfg, err := loadConfigForRoundSource(args, "stream-round-events")
	if err != nil {
		return err
	}
	client, err := newProtocolDaemonClient(cfg)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	return client.StreamRoundEvents(context.Background(), func(evt protocoldaemon.RoundEvent) error {
		return enc.Encode(evt)
	})
}

func validateRoundCloseRequest(req types.RoundCloseRequest) error {
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

func validateRoundCloseAgainstRoundSource(ctx context.Context, cfg *config.Config, req types.RoundCloseRequest) error {
	if cfg.RoundSource.ProtocolDaemonSocket == "" {
		return nil
	}
	roundID, err := strconv.ParseUint(req.RoundID, 10, 64)
	if err != nil {
		return fmt.Errorf("round close round_id must be numeric when protocol-daemon validation is enabled: %w", err)
	}
	client, err := newProtocolDaemonClient(cfg)
	if err != nil {
		return err
	}
	status, err := client.GetRoundStatus(ctx)
	if err != nil {
		return err
	}
	if status.LastRound == 0 {
		return nil
	}
	if roundID >= status.LastRound {
		return fmt.Errorf("round close round_id %d must be less than current observed round %d", roundID, status.LastRound)
	}
	return nil
}

func loadConfigForRoundSource(args []string, command string) (*config.Config, error) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to pool-reconciler config")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if *configPath == "" {
		return nil, errors.New("--config is required")
	}
	cfg, err := config.LoadFile(*configPath)
	if err != nil {
		return nil, err
	}
	if cfg.RoundSource.ProtocolDaemonSocket == "" {
		return nil, errors.New("round_source.protocol_daemon_socket is required for this command")
	}
	return cfg, nil
}

func newProtocolDaemonClient(cfg *config.Config) (*protocoldaemon.Client, error) {
	return protocoldaemon.NewClient(protocoldaemon.Config{
		SocketPath: cfg.RoundSource.ProtocolDaemonSocket,
		Timeout:    time.Duration(cfg.PoolController.TimeoutMS) * time.Millisecond,
	})
}

func newPaymentDaemonClient(cfg *config.Config) (*paymentdaemon.Client, error) {
	if cfg.PaymentDaemon.SocketPath == "" {
		return nil, errors.New("payment_daemon.socket is required for this command")
	}
	return paymentdaemon.NewClient(paymentdaemon.Config{
		SocketPath: cfg.PaymentDaemon.SocketPath,
		Timeout:    time.Duration(cfg.PaymentDaemon.TimeoutMS) * time.Millisecond,
	})
}

func prepareRoundCloseRequest(ctx context.Context, cfg *config.Config, explicitRoundID uint64) (types.RoundCloseRequest, error) {
	if cfg.RoundSource.ProtocolDaemonSocket == "" {
		return types.RoundCloseRequest{}, errors.New("round_source.protocol_daemon_socket is required for this command")
	}
	client, err := newProtocolDaemonClient(cfg)
	if err != nil {
		return types.RoundCloseRequest{}, err
	}
	status, err := client.GetRoundStatus(ctx)
	if err != nil {
		return types.RoundCloseRequest{}, err
	}
	if status.LastRound == 0 {
		return types.RoundCloseRequest{}, errors.New("protocol-daemon has not observed a round yet")
	}
	if status.LastRound == 1 && explicitRoundID == 0 {
		return types.RoundCloseRequest{}, errors.New("cannot prepare round close before round 1 has completed")
	}
	targetRound := status.LastRound - 1
	if explicitRoundID != 0 {
		if explicitRoundID >= status.LastRound {
			return types.RoundCloseRequest{}, fmt.Errorf("requested round_id %d must be less than current observed round %d", explicitRoundID, status.LastRound)
		}
		targetRound = explicitRoundID
	}
	req := types.RoundCloseRequest{
		ID:                     fmt.Sprintf("round-close-%d", targetRound),
		RoundID:                strconv.FormatUint(targetRound, 10),
		PoolRevenueWei:         "0",
		PoolCutWei:             "0",
		IncludedWorkReceiptIDs: []string{},
	}
	controllerClient, err := poolcontroller.NewClient(cfg.PoolController)
	if err != nil {
		return types.RoundCloseRequest{}, err
	}
	workReceipts, err := controllerClient.ListWorkReceipts(ctx, poolcontroller.ListWorkReceiptsOptions{
		RoundID: req.RoundID,
		Status:  "final",
		Limit:   500,
	})
	if err != nil {
		return types.RoundCloseRequest{}, err
	}
	req.IncludedWorkReceiptIDs = make([]string, 0, len(workReceipts))
	for _, receipt := range workReceipts {
		req.IncludedWorkReceiptIDs = append(req.IncludedWorkReceiptIDs, receipt.ID)
	}
	if cfg.PaymentDaemon.SocketPath != "" {
		paymentClient, err := newPaymentDaemonClient(cfg)
		if err != nil {
			return types.RoundCloseRequest{}, err
		}
		revenue, err := paymentClient.GetRoundRevenue(ctx, int64(targetRound))
		if err != nil {
			return types.RoundCloseRequest{}, err
		}
		req.PoolRevenueWei = revenue.ConfirmedRevenueWei
		req.PoolCutWei = commissionCutWei(revenue.ConfirmedRevenueWei, cfg.Pool.CommissionBPS)
	}
	return req, nil
}

func openStateRepo(cfg *config.Config) (*repo.StateRepo, error) {
	dir := filepath.Dir(cfg.Reconcile.StatePath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir state dir: %w", err)
		}
	}
	return repo.Open(cfg.Reconcile.StatePath)
}

func backfillClosedRounds(
	ctx context.Context,
	cfg *config.Config,
	protocolClient *protocoldaemon.Client,
	controllerClient *poolcontroller.Client,
	stateRepo *repo.StateRepo,
	enc *json.Encoder,
	opMu *sync.Mutex,
) error {
	status, err := protocolClient.GetRoundStatus(ctx)
	if err != nil {
		return err
	}
	if status.LastRound <= 1 {
		return nil
	}
	completed := status.LastRound - 1
	start := uint64(1)
	if cfg.Reconcile.BackfillLimit > 0 && completed > uint64(cfg.Reconcile.BackfillLimit) {
		start = completed - uint64(cfg.Reconcile.BackfillLimit) + 1
	}
	for roundID := start; roundID <= completed; roundID++ {
		opMu.Lock()
		record, found, err := stateRepo.GetRound(roundID)
		if err != nil {
			opMu.Unlock()
			return err
		}
		if found && record.Status == "closed" {
			if err := enc.Encode(map[string]any{
				"type":         "startup_backfill",
				"closed_round": roundID,
				"status":       "skipped",
				"reason":       "already closed",
			}); err != nil {
				opMu.Unlock()
				return err
			}
			opMu.Unlock()
			continue
		}
		result, err := attemptRoundClose(ctx, cfg, controllerClient, stateRepo, roundID)
		if err != nil {
			if err := enc.Encode(map[string]any{
				"type":         "startup_backfill",
				"closed_round": roundID,
				"status":       "error",
				"error":        err.Error(),
			}); err != nil {
				opMu.Unlock()
				return err
			}
			opMu.Unlock()
			continue
		}
		result["type"] = "startup_backfill"
		if err := enc.Encode(result); err != nil {
			opMu.Unlock()
			return err
		}
		opMu.Unlock()
	}
	return nil
}

func retryPendingRounds(
	ctx context.Context,
	cfg *config.Config,
	controllerClient *poolcontroller.Client,
	stateRepo *repo.StateRepo,
	enc *json.Encoder,
) error {
	pending, err := stateRepo.ListPendingRounds(cfg.Reconcile.BackfillLimit)
	if err != nil {
		return err
	}
	retryAfter := time.Duration(cfg.Reconcile.RetryInterval) * time.Millisecond
	for _, record := range pending {
		if !record.LastAttemptAt.IsZero() && time.Since(record.LastAttemptAt) < retryAfter {
			continue
		}
		result, err := attemptRoundClose(ctx, cfg, controllerClient, stateRepo, record.RoundID)
		if err != nil {
			if err := enc.Encode(map[string]any{
				"type":         "retry_tick",
				"closed_round": record.RoundID,
				"status":       "error",
				"error":        err.Error(),
			}); err != nil {
				return err
			}
			continue
		}
		result["type"] = "retry_tick"
		if err := enc.Encode(result); err != nil {
			return err
		}
	}
	return nil
}

func attemptRoundClose(
	ctx context.Context,
	cfg *config.Config,
	controllerClient *poolcontroller.Client,
	stateRepo *repo.StateRepo,
	explicitRoundID uint64,
) (map[string]any, error) {
	req, err := prepareRoundCloseRequest(ctx, cfg, explicitRoundID)
	if err != nil {
		return nil, err
	}
	roundID, err := strconv.ParseUint(req.RoundID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("prepared round_id %q must be numeric: %w", req.RoundID, err)
	}
	if err := stateRepo.MarkAttempt(roundID); err != nil {
		return nil, err
	}
	if err := validateRoundCloseRequest(req); err != nil {
		_ = stateRepo.MarkFailed(roundID, err.Error())
		return nil, err
	}
	if err := validateRoundCloseAgainstRoundSource(ctx, cfg, req); err != nil {
		_ = stateRepo.MarkFailed(roundID, err.Error())
		return nil, err
	}
	if err := controllerClient.SubmitRoundClose(ctx, req); err != nil {
		_ = stateRepo.MarkFailed(roundID, err.Error())
		return nil, err
	}
	if err := stateRepo.MarkClosed(roundID); err != nil {
		return nil, err
	}
	return map[string]any{
		"closed_round":       roundID,
		"status":             "closed",
		"work_receipt_count": len(req.IncludedWorkReceiptIDs),
		"pool_revenue_wei":   req.PoolRevenueWei,
	}, nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeJSONFile(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write output file: %w", err)
	}
	return nil
}

func commissionCutWei(revenueWei string, commissionBPS uint32) string {
	revenue, ok := new(big.Int).SetString(revenueWei, 10)
	if !ok || revenue.Sign() <= 0 || commissionBPS == 0 {
		return "0"
	}
	cut := new(big.Int).Mul(revenue, big.NewInt(int64(commissionBPS)))
	cut.Quo(cut, big.NewInt(10000))
	return cut.String()
}

func usageError(w io.Writer) error {
	_, _ = fmt.Fprintln(w, "usage: livepeer-pool-reconciler <close-round|watch-rounds|prepare-round-close|get-round-revenue|submit-round-close|get-round-status|stream-round-events|version> [flags]")
	return errors.New("invalid command")
}
