package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/observability"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	adminserver "github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/server/admin"
	memberserver "github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/server/member"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/backendverify"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokerrender"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/configgen"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/legacyimport"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/probes"
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
	case "import-legacy-config":
		return runImportLegacyConfig(args[1:], stdout)
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

func runImportLegacyConfig(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("import-legacy-config", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	configPath := fs.String("config", "", "path to legacy pool-controller config")
	dataDir := fs.String("data-dir", "", "pool-controller data directory")
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
	stateRepo, err := repo.Open(*dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = stateRepo.Close() }()

	built, err := legacyimport.Build(cfg, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := legacyimport.Persist(stateRepo, built, "import-legacy-config", time.Now().UTC()); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "imported offers=%d members=%d backends=%d assignments=%d\n", len(built.Offers), len(built.Members), len(built.Backends), len(built.Assignments))
	return nil
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
	stateRepo, err := repo.Open(*dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = stateRepo.Close() }()

	if err := maybeImportLegacyConfig(stateRepo, cfg); err != nil {
		return err
	}
	rendered, runtimeInfo, err := renderBrokerState(stateRepo, cfg)
	if err != nil {
		return err
	}
	state := &runtimeState{configPath: *configPath, repo: stateRepo, adminToken: adminToken}
	if err := state.Replace(cfg, rendered, "startup", runtimeInfo); err != nil {
		return err
	}
	probeCtx, cancelProbes := context.WithCancel(context.Background())
	defer cancelProbes()
	go runSyntheticProbeLoop(probeCtx, state, stderr)

	paidAddr := *listenAddr
	if paidAddr == ":8080" && cfg.Listen.Paid != "" {
		paidAddr = cfg.Listen.Paid
	}
	metricsAddr := cfg.Listen.Metrics
	if metricsAddr == "" {
		metricsAddr = ":9090"
	}

	srv := &http.Server{
		Addr:    paidAddr,
		Handler: newServeMux(state),
	}
	metricsSrv := newMetricsServer(metricsAddr)

	errCh := make(chan error, 2)
	go func() {
		_, _ = fmt.Fprintf(stdout, "listening on %s\n", metricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("listen metrics: %w", err)
		}
	}()
	_, _ = fmt.Fprintf(stdout, "listening on %s\n", paidAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func maybeImportLegacyConfig(stateRepo *repo.StateRepo, cfg *config.Config) error {
	if stateRepo == nil || cfg == nil {
		return nil
	}
	importCfg := cfg
	switch {
	case cfg.Bootstrap.ImportLegacyConfigPath != "":
		loaded, err := config.LoadFile(cfg.Bootstrap.ImportLegacyConfigPath)
		if err != nil {
			return fmt.Errorf("load bootstrap.import_legacy_config_path: %w", err)
		}
		importCfg = loaded
	case !cfg.Bootstrap.AutoImportLegacyConfig:
		return nil
	}
	if len(importCfg.Members) == 0 {
		return nil
	}
	built, err := legacyimport.Build(importCfg, time.Now().UTC())
	if err != nil {
		return err
	}
	return legacyimport.Persist(stateRepo, built, "legacy-config-sync", time.Now().UTC())
}

func renderBrokerState(stateRepo *repo.StateRepo, cfg *config.Config) ([]byte, *types.DesiredBrokerRuntime, error) {
	if stateRepo == nil {
		return nil, nil, fmt.Errorf("state repo is nil")
	}
	offers, err := stateRepo.ListOffers()
	if err != nil {
		return nil, nil, err
	}
	members, err := stateRepo.ListMembers()
	if err != nil {
		return nil, nil, err
	}
	backends, err := stateRepo.ListMemberBackends()
	if err != nil {
		return nil, nil, err
	}
	assignments, err := stateRepo.ListAssignments()
	if err != nil {
		return nil, nil, err
	}
	result, err := brokerrender.Render(brokerrender.RenderInput{
		Bootstrap: brokerrender.BootstrapBrokerSettings{
			Identity:      cfg.Identity,
			Listen:        cfg.Listen,
			PaymentDaemon: cfg.PaymentDaemon,
			ReceiptSink:   cfg.ReceiptSink,
		},
		Offers:      offers,
		Members:     members,
		Backends:    backends,
		Assignments: assignments,
	})
	if err != nil {
		return nil, nil, err
	}
	runtimeInfo := &types.DesiredBrokerRuntime{
		Revision:        result.Revision,
		RenderedYAML:    string(result.ConfigYAML),
		RenderedAt:      time.Now().UTC(),
		OfferCount:      len(offers),
		MemberCount:     len(members),
		BackendCount:    len(backends),
		AssignmentCount: len(assignments),
	}
	return result.ConfigYAML, runtimeInfo, nil
}

type runtimeState struct {
	mu         sync.RWMutex
	configPath string
	repo       *repo.StateRepo
	adminToken string
	cfg        *config.Config
	rendered   []byte
	latest     *repo.Snapshot
	runtime    *types.DesiredBrokerRuntime
}

func (s *runtimeState) Replace(cfg *config.Config, rendered []byte, source string, runtimeInfo *types.DesiredBrokerRuntime) error {
	var latest *repo.Snapshot
	if s.repo != nil {
		if err := maybeImportLegacyConfig(s.repo, cfg); err != nil {
			return err
		}
		repo.ApplyBackendSelectionSettings(cfg.Scoring)
		offers, err := s.repo.ListOffers()
		if err != nil {
			return fmt.Errorf("list offers for backend selection sync: %w", err)
		}
		members, err := s.repo.ListMembers()
		if err != nil {
			return fmt.Errorf("list members for backend selection sync: %w", err)
		}
		backends, err := s.repo.ListMemberBackends()
		if err != nil {
			return fmt.Errorf("list member backends for backend selection sync: %w", err)
		}
		assignments, err := s.repo.ListAssignments()
		if err != nil {
			return fmt.Errorf("list assignments for backend selection sync: %w", err)
		}
		if err := s.repo.SyncBackendSelectionStatesFromEntities(offers, members, backends, assignments); err != nil {
			return fmt.Errorf("sync backend selection state: %w", err)
		}
		configRaw, err := os.ReadFile(s.configPath)
		if err != nil {
			return fmt.Errorf("read active config for snapshot: %w", err)
		}
		snap := repo.Snapshot{
			ID:                 time.Now().UTC().Format(time.RFC3339Nano),
			CreatedAt:          time.Now().UTC(),
			Source:             source,
			MemberCount:        len(members),
			RenderedBytes:      len(rendered),
			ConfigYAML:         string(configRaw),
			RenderedBrokerYAML: string(rendered),
		}
		if err := s.repo.SaveSnapshot(snap); err != nil {
			return err
		}
		if runtimeInfo != nil {
			if err := s.repo.PutDesiredBrokerRuntime(*runtimeInfo); err != nil {
				return err
			}
		}
		latest = &snap
	}
	s.mu.Lock()
	s.cfg = cfg
	s.rendered = append([]byte(nil), rendered...)
	s.latest = latest
	if runtimeInfo != nil {
		copyRuntime := *runtimeInfo
		s.runtime = &copyRuntime
	}
	s.mu.Unlock()
	if err := s.syncSelectionMetrics(); err != nil {
		return err
	}
	return s.syncAccountingMetrics()
}

func (s *runtimeState) Snapshot() (*config.Config, []byte, *repo.Snapshot, *types.DesiredBrokerRuntime) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest *repo.Snapshot
	if s.latest != nil {
		copySnap := *s.latest
		latest = &copySnap
	}
	var runtimeInfo *types.DesiredBrokerRuntime
	if s.runtime != nil {
		copyRuntime := *s.runtime
		runtimeInfo = &copyRuntime
	}
	return s.cfg, append([]byte(nil), s.rendered...), latest, runtimeInfo
}

func (s *runtimeState) BackendSelectionSnapshot() (types.BackendSelectionSnapshot, error) {
	items, err := s.repo.ListBackendSelectionStates()
	if err != nil {
		return types.BackendSelectionSnapshot{}, err
	}
	cfg, _, _, _ := s.Snapshot()
	scoring := config.Scoring{}
	if cfg != nil {
		scoring = cfg.Scoring
	}
	recentWindowStaleAfterSeconds := 0.0
	if scoring.RecentWindowStaleAfterMS > 0 {
		recentWindowStaleAfterSeconds = float64(scoring.RecentWindowStaleAfterMS) / 1000.0
	}
	return types.BackendSelectionSnapshot{
		GeneratedAt:                   time.Now().UTC(),
		Version:                       1,
		CooldownDurationSeconds:       float64(scoring.CooldownDurationMS) / 1000.0,
		CooldownFailureTrigger:        scoring.CooldownFailureTrigger,
		EMAHalfLifeSeconds:            float64(scoring.EMAHalfLifeMS) / 1000.0,
		LatencyTargetMS:               scoring.LatencyTargetMS,
		RecentWindowStaleAfterSeconds: recentWindowStaleAfterSeconds,
		WindowScoreWeight:             scoring.WindowScoreWeight,
		EMAScoreWeight:                scoring.EMAScoreWeight,
		WarmupModifier:                scoring.WarmupModifier,
		WarmupExitSamples:             scoring.WarmupExitSamples,
		Entries:                       items,
	}, nil
}

func (s *runtimeState) BackendSelectionSummary() (backendSelectionSummaryView, error) {
	items, err := s.repo.ListBackendSelectionStates()
	if err != nil {
		return backendSelectionSummaryView{}, err
	}
	cfg, _, _, _ := s.Snapshot()
	if cfg == nil {
		return buildBackendSelectionSummary(items, config.Scoring{}), nil
	}
	return buildBackendSelectionSummary(items, cfg.Scoring), nil
}

func (s *runtimeState) GetBackendSelectionState(memberEthAddress, backendID, capabilityID, offeringID string) (types.BackendSelectionState, error) {
	return s.repo.GetBackendSelectionState(memberEthAddress, backendID, capabilityID, offeringID)
}

func (s *runtimeState) SaveBackendSelectionState(item types.BackendSelectionState) error {
	item.UpdatedAt = time.Now().UTC()
	if err := s.repo.SaveBackendSelectionState(item); err != nil {
		return err
	}
	return s.syncSelectionMetrics()
}

func (s *runtimeState) ApplyBackendOutcome(outcome types.BackendOutcome) (types.BackendSelectionState, error) {
	item, err := s.repo.ApplyBackendOutcome(outcome)
	if err != nil {
		return item, err
	}
	observability.RecordBackendOutcomeIngest(outcome)
	return item, s.syncSelectionMetrics()
}

func (s *runtimeState) ApplySyntheticProbeObservation(observation types.SyntheticProbeObservation) (types.BackendSelectionState, error) {
	item, err := s.repo.ApplySyntheticProbeObservation(observation)
	if err != nil {
		return item, err
	}
	return item, s.syncSelectionMetrics()
}

func (s *runtimeState) RunSyntheticProbesOnce(ctx context.Context) (probes.RunSummary, error) {
	cfg, _, _, _ := s.Snapshot()
	if cfg == nil {
		return probes.RunSummary{}, fmt.Errorf("config is not loaded")
	}
	timeout := time.Duration(cfg.SyntheticProbes.TimeoutMS) * time.Millisecond
	runner := probes.NewRunner(timeout)
	startedAt := time.Now().UTC()
	offers, err := s.repo.ListOffers()
	if err != nil {
		return probes.RunSummary{}, err
	}
	members, err := s.repo.ListMembers()
	if err != nil {
		return probes.RunSummary{}, err
	}
	backends, err := s.repo.ListMemberBackends()
	if err != nil {
		return probes.RunSummary{}, err
	}
	assignments, err := s.repo.ListAssignments()
	if err != nil {
		return probes.RunSummary{}, err
	}
	summary, err := runner.RunOnceTargets(ctx, buildSyntheticProbeTargets(offers, members, backends, assignments), s.ApplySyntheticProbeObservation)
	duration := time.Since(startedAt)
	if err != nil {
		observability.RecordSyntheticProbeRunSummary(nil, duration, "error")
		return summary, err
	}
	results := make([]observability.ProbeResultMetric, 0, len(summary.Results))
	for _, result := range summary.Results {
		results = append(results, observability.NewProbeResultMetric(
			result.CapabilityID,
			result.OfferingID,
			result.Status,
			result.Reason,
		))
	}
	runResult := "completed"
	if summary.Failed > 0 && summary.Succeeded == 0 && summary.Applied == 0 {
		runResult = "failed"
	} else if summary.Failed > 0 {
		runResult = "partial"
	}
	observability.RecordSyntheticProbeRunSummary(results, duration, runResult)
	return summary, nil
}

func buildSyntheticProbeTargets(offers []types.Offer, members []types.MemberRecord, backends []types.MemberBackend, assignments []types.Assignment) []probes.ProbeTarget {
	offersByID := make(map[string]types.Offer, len(offers))
	for _, offer := range offers {
		offersByID[offer.ID] = offer
	}
	membersByID := make(map[string]types.MemberRecord, len(members))
	for _, member := range members {
		membersByID[member.ID] = member
	}
	backendsByID := make(map[string]types.MemberBackend, len(backends))
	for _, backend := range backends {
		backendsByID[backend.ID] = backend
	}

	targets := make([]probes.ProbeTarget, 0)
	for _, assignment := range assignments {
		if assignment.Status != types.AssignmentStatusActive {
			continue
		}
		offer, ok := offersByID[assignment.OfferID]
		if !ok || offer.Status != types.OfferStatusActive {
			continue
		}
		backend, ok := backendsByID[assignment.MemberBackendID]
		if !ok || backend.Status != types.BackendStatusActive {
			continue
		}
		member, ok := membersByID[backend.MemberID]
		if !ok || member.Status != types.MemberStatusActive {
			continue
		}
		targets = append(targets, probes.ProbeTarget{
			Member: config.Member{
				EthAddress: member.EthAddress,
			},
			Backend: config.Backend{
				ID:        backend.ID,
				Transport: backend.Transport,
				URL:       backend.URL,
				Auth:      backend.Auth,
			},
			Offering: config.Offering{
				CapabilityID:    offer.CapabilityID,
				OfferingID:      offer.OfferingID,
				InteractionMode: offer.InteractionMode,
				WorkUnit:        offer.WorkUnit,
				Price:           offer.Price,
				Extra:           offer.Extra,
				Constraints:     offer.Constraints,
				Health:          config.Health{Probe: backend.HealthProbe},
			},
		})
	}
	return targets
}

func (s *runtimeState) Reload() error {
	cfg, err := config.LoadFile(s.configPath)
	if err != nil {
		return err
	}
	if err := maybeImportLegacyConfig(s.repo, cfg); err != nil {
		return err
	}
	rendered, runtimeInfo, err := renderBrokerState(s.repo, cfg)
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
	return s.Replace(cfg, rendered, "reload", runtimeInfo)
}

func (s *runtimeState) RefreshRenderedFromState(source string) error {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if cfg == nil {
		return fmt.Errorf("config is not loaded")
	}
	rendered, runtimeInfo, err := renderBrokerState(s.repo, cfg)
	if err != nil {
		return err
	}
	return s.Replace(cfg, rendered, source, runtimeInfo)
}

func (s *runtimeState) ApplyDesiredRuntime(desired *types.DesiredBrokerRuntime) error {
	if desired == nil || strings.TrimSpace(desired.Revision) == "" {
		return fmt.Errorf("desired broker runtime is not available")
	}
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if cfg == nil {
		return fmt.Errorf("config is not loaded")
	}
	if err := runBrokerApplyCommand(cfg, s.configPath, desired); err != nil {
		return err
	}
	if err := s.RefreshRenderedFromState("broker-runtime-apply"); err != nil {
		return err
	}
	_, _, _, current := s.Snapshot()
	if current == nil || strings.TrimSpace(current.Revision) == "" {
		return fmt.Errorf("desired broker runtime is not available after apply refresh")
	}
	if current.Revision != desired.Revision {
		return fmt.Errorf("desired broker runtime changed during apply: expected %s got %s", desired.Revision, current.Revision)
	}
	if strings.TrimSpace(cfg.Bootstrap.BrokerAdminURL) != "" {
		client := brokeradmin.New(
			cfg.Bootstrap.BrokerAdminURL,
			cfg.Bootstrap.BrokerAdminAuth,
			time.Duration(cfg.Bootstrap.BrokerAdminTimeoutMS)*time.Millisecond,
		)
		status, err := client.ReloadAndConfirm(current.Revision)
		if status != nil {
			if recordErr := s.recordBrokerRuntimeStatus(status); recordErr != nil {
				return recordErr
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *runtimeState) recordBrokerRuntimeStatus(status *brokeradmin.RuntimeStatus) error {
	if status == nil || s.repo == nil {
		return nil
	}
	applied, _ := s.repo.GetAppliedBrokerRuntime()
	applied.BrokerReloadAttemptID = strings.TrimSpace(status.LastReloadAttemptID)
	applied.BrokerLoadedRevision = strings.TrimSpace(status.LoadedRevision)
	applied.BrokerLoadedAt = status.LoadedAt
	applied.BrokerReloadStatus = strings.TrimSpace(status.LastReloadStatus)
	applied.BrokerReloadError = strings.TrimSpace(status.LastReloadError)
	return s.repo.PutAppliedBrokerRuntime(applied)
}

func runBrokerApplyCommand(cfg *config.Config, configPath string, desired *types.DesiredBrokerRuntime) error {
	if cfg == nil || len(cfg.Bootstrap.BrokerApplyCommand) == 0 {
		return nil
	}
	if desired == nil || strings.TrimSpace(desired.Revision) == "" {
		return fmt.Errorf("desired broker runtime is not available")
	}
	tmpFile, err := os.CreateTemp("", "pool-controller-broker-config-*.yaml")
	if err != nil {
		return fmt.Errorf("create broker apply temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	if _, err := tmpFile.WriteString(desired.RenderedYAML); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write broker apply temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close broker apply temp file: %w", err)
	}
	timeout := time.Duration(cfg.Bootstrap.BrokerApplyTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := append([]string(nil), cfg.Bootstrap.BrokerApplyCommand...)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	hash := sha256.Sum256([]byte(desired.RenderedYAML))
	cmd.Env = append(os.Environ(),
		"POOL_CONTROLLER_CONFIG_PATH="+configPath,
		"POOL_CONTROLLER_BROKER_CONFIG_PATH="+tmpPath,
		"POOL_CONTROLLER_BROKER_DESIRED_REVISION="+desired.Revision,
		"POOL_CONTROLLER_BROKER_CONFIG_SHA256="+fmt.Sprintf("%x", hash[:]),
	)
	cmd.Stdin = strings.NewReader(desired.RenderedYAML)
	output, err := cmd.CombinedOutput()
	trimmedOutput := strings.TrimSpace(string(output))
	if ctx.Err() == context.DeadlineExceeded {
		if trimmedOutput != "" {
			return fmt.Errorf("broker apply command timed out after %s: %s", timeout, trimmedOutput)
		}
		return fmt.Errorf("broker apply command timed out after %s", timeout)
	}
	if err != nil {
		if trimmedOutput != "" {
			return fmt.Errorf("broker apply command failed: %w: %s", err, trimmedOutput)
		}
		return fmt.Errorf("broker apply command failed: %w", err)
	}
	return nil
}

func (s *runtimeState) syncSelectionMetrics() error {
	cfg, _, _, _ := s.Snapshot()
	if cfg == nil {
		return nil
	}
	items, err := s.repo.ListBackendSelectionStates()
	if err != nil {
		return err
	}
	observability.UpdateScoringSettings(cfg.Scoring)
	observability.UpdateBackendSelectionSnapshot(items, time.Now().UTC())
	return nil
}

func (s *runtimeState) syncAccountingMetrics() error {
	workReceipts, err := s.repo.ListWorkReceipts(0)
	if err != nil {
		return err
	}
	roundReceipts, err := s.repo.ListRoundReceipts(0)
	if err != nil {
		return err
	}
	payoutIntents, err := s.repo.ListPayoutIntents(0)
	if err != nil {
		return err
	}
	observability.UpdateAccountingSnapshot(workReceipts, roundReceipts, payoutIntents)
	return nil
}

func countPersistedEntities(stateRepo *repo.StateRepo) (memberCount, backendCount, offerCount int, err error) {
	if stateRepo == nil {
		return 0, 0, 0, nil
	}
	members, err := stateRepo.ListMembers()
	if err != nil {
		return 0, 0, 0, err
	}
	backends, err := stateRepo.ListMemberBackends()
	if err != nil {
		return 0, 0, 0, err
	}
	offers, err := stateRepo.ListOffers()
	if err != nil {
		return 0, 0, 0, err
	}
	return len(members), len(backends), len(offers), nil
}

func newServeMux(state *runtimeState) *http.ServeMux {
	mux := http.NewServeMux()
	verifier := backendverify.New(state.repo)
	memberserver.Register(mux, memberserver.Deps{
		Repo:     state.repo,
		Verifier: verifier,
	})
	adminserver.Register(mux, adminserver.Deps{
		Repo:            state.repo,
		WrapAuth:        func(next http.HandlerFunc) http.HandlerFunc { return withAdminAuth(state, next) },
		RefreshRendered: func(source string) error { return state.RefreshRenderedFromState(source) },
		GetRuntimeApplyInfo: func() adminserver.RuntimeApplyInfo {
			cfg, _, _, _ := state.Snapshot()
			info := adminserver.RuntimeApplyInfo{Mode: "controller-refresh"}
			if cfg == nil {
				return info
			}
			info.CommandConfigured = len(cfg.Bootstrap.BrokerApplyCommand) > 0
			info.BrokerAdminConfigured = strings.TrimSpace(cfg.Bootstrap.BrokerAdminURL) != ""
			switch {
			case info.CommandConfigured && info.BrokerAdminConfigured:
				info.Mode = "command+broker-admin"
				info.TimeoutMS = cfg.Bootstrap.BrokerApplyTimeoutMS
				if cfg.Bootstrap.BrokerAdminTimeoutMS > info.TimeoutMS {
					info.TimeoutMS = cfg.Bootstrap.BrokerAdminTimeoutMS
				}
			case info.CommandConfigured:
				info.Mode = "command"
				info.TimeoutMS = cfg.Bootstrap.BrokerApplyTimeoutMS
			case info.BrokerAdminConfigured:
				info.Mode = "broker-admin"
				info.TimeoutMS = cfg.Bootstrap.BrokerAdminTimeoutMS
			}
			return info
		},
		ApplyDesiredRuntime: func(desired *types.DesiredBrokerRuntime) error { return state.ApplyDesiredRuntime(desired) },
		Verifier:            verifier,
		GetBrokerConfig: func() []byte {
			_, rendered, _, _ := state.Snapshot()
			return rendered
		},
		GetMembersJSON: func() ([]byte, error) {
			members, err := buildMemberViewsFromState(state.repo)
			if err != nil {
				return nil, err
			}
			return json.Marshal(struct {
				Members []memberView `json:"members"`
			}{Members: members})
		},
		GetOfferingsJSON: func() ([]byte, error) {
			offerings, err := buildOfferingViewsFromState(state.repo)
			if err != nil {
				return nil, err
			}
			return json.Marshal(struct {
				Offerings []offeringView `json:"offerings"`
			}{Offerings: offerings})
		},
		GetStateJSON: func() ([]byte, error) {
			cfg, rendered, latest, runtimeInfo := state.Snapshot()
			scoring := config.Scoring{}
			if cfg != nil {
				scoring = cfg.Scoring
			}
			return json.Marshal(struct {
				MemberCount   int              `json:"member_count"`
				RenderedBytes int              `json:"rendered_bytes"`
				Scoring       config.Scoring   `json:"scoring"`
				Latest        *snapshotSummary `json:"latest_snapshot,omitempty"`
			}{
				MemberCount: func() int {
					if runtimeInfo != nil && runtimeInfo.MemberCount > 0 {
						return runtimeInfo.MemberCount
					}
					memberCount, _, _, err := countPersistedEntities(state.repo)
					if err != nil {
						return 0
					}
					return memberCount
				}(),
				RenderedBytes: len(rendered),
				Scoring:       scoring,
				Latest:        summarizeSnapshot(latest),
			})
		},
		GetDesiredRuntime: func() (*types.DesiredBrokerRuntime, error) {
			_, _, _, runtimeInfo := state.Snapshot()
			return runtimeInfo, nil
		},
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ready\n")
	})
	mux.HandleFunc("GET /public/v1/summary", func(w http.ResponseWriter, _ *http.Request) {
		cfg, _, _, runtimeInfo := state.Snapshot()
		rounds, err := state.repo.ListRoundReceipts(1)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		backendSelectionSummary, err := state.BackendSelectionSummary()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		backendCount := 0
		memberCount := 0
		offeringCount := 0
		scoring := config.Scoring{}
		if cfg != nil {
			scoring = cfg.Scoring
		}
		if runtimeInfo != nil {
			memberCount = runtimeInfo.MemberCount
			backendCount = runtimeInfo.BackendCount
			offeringCount = runtimeInfo.OfferCount
		} else {
			memberCount, backendCount, offeringCount, err = countPersistedEntities(state.repo)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		latestRoundID := ""
		if len(rounds) > 0 {
			latestRoundID = rounds[0].RoundID
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			MemberCount       int                       `json:"member_count"`
			BackendCount      int                       `json:"backend_count"`
			OfferingCount     int                       `json:"offering_count"`
			LatestClosedRound string                    `json:"latest_closed_round,omitempty"`
			WorstOfferings    []publicWorstOfferingView `json:"worst_offerings,omitempty"`
		}{
			MemberCount:       memberCount,
			BackendCount:      backendCount,
			OfferingCount:     offeringCount,
			LatestClosedRound: latestRoundID,
			WorstOfferings:    buildPublicWorstOfferingViews(backendSelectionSummary.WorstOfferings, scoring.PublicWorstOfferingsLimit),
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
		offerings, err := buildOfferingViewsFromState(state.repo)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Offerings []offeringView `json:"offerings"`
		}{Offerings: offerings})
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
		items, err = normalizeExpiredPayoutIntentLeases(state.repo, items, time.Now().UTC())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = state.syncAccountingMetrics()
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
	mux.HandleFunc("GET /admin/v1/scoring-settings", withAdminAuth(state, func(w http.ResponseWriter, _ *http.Request) {
		cfg, _, _, _ := state.Snapshot()
		scoring := config.Scoring{}
		if cfg != nil {
			scoring = cfg.Scoring
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Scoring config.Scoring `json:"scoring"`
		}{Scoring: scoring})
	}))
	mux.HandleFunc("GET /admin/v1/backend-selection-snapshot", withAdminAuth(state, func(w http.ResponseWriter, _ *http.Request) {
		snapshot, err := state.BackendSelectionSnapshot()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(snapshot)
	}))
	mux.HandleFunc("GET /admin/v1/backend-selection-summary", withAdminAuth(state, func(w http.ResponseWriter, _ *http.Request) {
		summary, err := state.BackendSelectionSummary()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(summary)
	}))
	mux.HandleFunc("POST /admin/v1/backend-overrides/quarantine", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		applyBackendSelectionOverride(state, w, r, func(item *types.BackendSelectionState, req backendOverrideRequest) error {
			item.State = types.BackendSelectionStateQuarantined
			item.ExclusionReason = defaultString(req.Reason, "operator_quarantine")
			return nil
		})
	}))
	mux.HandleFunc("POST /admin/v1/backend-overrides/clear-quarantine", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		applyBackendSelectionOverride(state, w, r, func(item *types.BackendSelectionState, _ backendOverrideRequest) error {
			if item.State == types.BackendSelectionStateQuarantined {
				item.State = types.BackendSelectionStateEligible
			}
			if item.ExclusionReason == "operator_quarantine" {
				item.ExclusionReason = ""
			}
			return nil
		})
	}))
	mux.HandleFunc("POST /admin/v1/backend-overrides/drain", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		applyBackendSelectionOverride(state, w, r, func(item *types.BackendSelectionState, req backendOverrideRequest) error {
			item.State = types.BackendSelectionStateExcluded
			item.ExclusionReason = defaultString(req.Reason, "operator_drain")
			return nil
		})
	}))
	mux.HandleFunc("POST /admin/v1/backend-overrides/clear-drain", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		applyBackendSelectionOverride(state, w, r, func(item *types.BackendSelectionState, _ backendOverrideRequest) error {
			if item.ExclusionReason == "operator_drain" {
				item.ExclusionReason = ""
				if item.State == types.BackendSelectionStateExcluded {
					item.State = types.BackendSelectionStateEligible
				}
			}
			return nil
		})
	}))
	mux.HandleFunc("POST /admin/v1/backend-overrides/warmup", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		applyBackendSelectionOverride(state, w, r, func(item *types.BackendSelectionState, req backendOverrideRequest) error {
			if req.WarmupModifier == nil || *req.WarmupModifier < 0 {
				return fmt.Errorf("warmup_modifier must be >= 0")
			}
			override := *req.WarmupModifier
			item.WarmupOverride = &override
			item.WarmupSource = "manual_override"
			item.WarmupModifier = override
			return nil
		})
	}))
	mux.HandleFunc("POST /admin/v1/backend-overrides/clear-warmup", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		applyBackendSelectionOverride(state, w, r, func(item *types.BackendSelectionState, _ backendOverrideRequest) error {
			item.WarmupOverride = nil
			return nil
		})
	}))
	mux.HandleFunc("POST /admin/v1/backend-overrides/max-share-cap", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		applyBackendSelectionOverride(state, w, r, func(item *types.BackendSelectionState, req backendOverrideRequest) error {
			if req.MaxShareCap == nil || *req.MaxShareCap < 0 || *req.MaxShareCap > 1 {
				return fmt.Errorf("max_share_cap must be between 0 and 1")
			}
			item.MaxShareCap = *req.MaxShareCap
			return nil
		})
	}))
	mux.HandleFunc("POST /admin/v1/backend-overrides/clear-max-share-cap", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		applyBackendSelectionOverride(state, w, r, func(item *types.BackendSelectionState, _ backendOverrideRequest) error {
			item.MaxShareCap = 0
			return nil
		})
	}))
	mux.HandleFunc("POST /admin/v1/backend-outcomes", withAdminAuth(state, func(w http.ResponseWriter, r *http.Request) {
		var outcome types.BackendOutcome
		if err := json.NewDecoder(r.Body).Decode(&outcome); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := validateBackendOutcome(outcome); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		item, err := state.ApplyBackendOutcome(outcome)
		if err != nil {
			if strings.Contains(err.Error(), ": not found") {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			if strings.Contains(err.Error(), "unsupported outcome") {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Status string                      `json:"status"`
			Item   types.BackendSelectionState `json:"item"`
		}{
			Status: "ingested",
			Item:   item,
		})
	}))
	mux.HandleFunc("POST /admin/v1/synthetic-probes/run", withAdminAuth(state, func(w http.ResponseWriter, _ *http.Request) {
		summary, err := state.RunSyntheticProbesOnce(context.Background())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Status  string            `json:"status"`
			Summary probes.RunSummary `json:"summary"`
		}{
			Status:  "completed",
			Summary: summary,
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
		observability.RecordReceiptWrite("work", receipt.Status, 1)
		if err := state.syncAccountingMetrics(); err != nil {
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
		_ = state.syncAccountingMetrics()
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
		_ = state.syncAccountingMetrics()
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
		_ = state.syncAccountingMetrics()
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
		_ = state.syncAccountingMetrics()
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
		observability.RecordReceiptWrite("round", "upserted", 1)
		if err := state.syncAccountingMetrics(); err != nil {
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
		observability.RecordPayoutIntentAction("derive", "pending", len(intents))
		if err := state.syncAccountingMetrics(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
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
		observability.RecordPayoutIntentAction("export", "exported", len(items))
		if err := state.syncAccountingMetrics(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
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
		observability.RecordPayoutIntentAction("claim", "leased", len(claimed))
		if err := state.syncAccountingMetrics(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
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
		observability.RecordPayoutIntentAction("renew", "leased", len(renewed))
		if err := state.syncAccountingMetrics(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
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
		observability.RecordPayoutIntentAction("release", "exported", len(released))
		if err := state.syncAccountingMetrics(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
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
		observability.RecordPayoutIntentAction("requeue", "pending", len(requeued))
		if err := state.syncAccountingMetrics(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
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
		observability.RecordPayoutIntentAction("status_update", req.Status, len(updated))
		if err := state.syncAccountingMetrics(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
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
		observability.RecordReceiptWrite("round", "closed", 1)
		if err := state.syncAccountingMetrics(); err != nil {
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
		_, rendered, latest, runtimeInfo := state.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			MemberCount   int    `json:"member_count"`
			RenderedBytes int    `json:"rendered_bytes"`
			Status        string `json:"status"`
			SnapshotID    string `json:"snapshot_id,omitempty"`
		}{
			MemberCount: func() int {
				if runtimeInfo != nil && runtimeInfo.MemberCount > 0 {
					return runtimeInfo.MemberCount
				}
				memberCount, _, _, err := countPersistedEntities(state.repo)
				if err != nil {
					return 0
				}
				return memberCount
			}(),
			RenderedBytes: len(rendered),
			Status:        "reloaded",
			SnapshotID:    latest.ID,
		})
	}))
	return mux
}

func runSyntheticProbeLoop(ctx context.Context, state *runtimeState, stderr io.Writer) {
	interval := 5 * time.Second
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			cfg, _, _, _ := state.Snapshot()
			if cfg == nil || !cfg.SyntheticProbes.Enabled {
				timer.Reset(interval)
				continue
			}
			if next := time.Duration(cfg.SyntheticProbes.IntervalMS) * time.Millisecond; next > 0 {
				interval = next
			}
			summary, err := state.RunSyntheticProbesOnce(ctx)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "synthetic probe run error: %v\n", err)
				timer.Reset(interval)
				continue
			}
			_, _ = fmt.Fprintf(stderr, "synthetic probe run completed: applied=%d succeeded=%d failed=%d skipped=%d\n", summary.Applied, summary.Succeeded, summary.Failed, summary.Skipped)
			timer.Reset(interval)
		}
	}
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

func applyBackendSelectionOverride(state *runtimeState, w http.ResponseWriter, r *http.Request, mutate func(*types.BackendSelectionState, backendOverrideRequest) error) {
	var req backendOverrideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.MemberEthAddress = strings.TrimSpace(req.MemberEthAddress)
	req.BackendID = strings.TrimSpace(req.BackendID)
	req.CapabilityID = strings.TrimSpace(req.CapabilityID)
	req.OfferingID = strings.TrimSpace(req.OfferingID)
	if req.MemberEthAddress == "" || req.BackendID == "" || req.CapabilityID == "" || req.OfferingID == "" {
		http.Error(w, "member_eth_address, backend_id, capability_id, and offering_id are required", http.StatusBadRequest)
		return
	}

	item, err := state.GetBackendSelectionState(req.MemberEthAddress, req.BackendID, req.CapabilityID, req.OfferingID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := mutate(&item, req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := state.SaveBackendSelectionState(item); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(item)
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func offerFromRequest(req offerMutationRequest) (types.Offer, error) {
	req.ID = strings.TrimSpace(req.ID)
	req.CapabilityID = strings.TrimSpace(req.CapabilityID)
	req.OfferingID = strings.TrimSpace(req.OfferingID)
	req.InteractionMode = strings.TrimSpace(req.InteractionMode)
	if req.ID == "" {
		return types.Offer{}, fmt.Errorf("id is required")
	}
	if req.CapabilityID == "" || req.OfferingID == "" || req.InteractionMode == "" {
		return types.Offer{}, fmt.Errorf("capability_id, offering_id, and interaction_mode are required")
	}
	if req.WorkUnit.Name == "" || len(req.WorkUnit.Extractor) == 0 {
		return types.Offer{}, fmt.Errorf("work_unit.name and work_unit.extractor are required")
	}
	if req.Price.AmountWei == "" || req.Price.PerUnits == 0 {
		return types.Offer{}, fmt.Errorf("price.amount_wei and price.per_units > 0 are required")
	}
	status := types.OfferStatusActive
	if strings.TrimSpace(req.Status) != "" {
		status = types.OfferStatus(strings.TrimSpace(req.Status))
	}
	return types.Offer{
		ID:              req.ID,
		CapabilityID:    req.CapabilityID,
		OfferingID:      req.OfferingID,
		InteractionMode: req.InteractionMode,
		WorkUnit:        req.WorkUnit,
		Price:           req.Price,
		Extra:           req.Extra,
		Constraints:     req.Constraints,
		Status:          status,
	}, nil
}

func updatedOfferFromRequest(current types.Offer, req offerMutationRequest) (types.Offer, error) {
	if strings.TrimSpace(req.CapabilityID) != "" {
		current.CapabilityID = strings.TrimSpace(req.CapabilityID)
	}
	if strings.TrimSpace(req.OfferingID) != "" {
		current.OfferingID = strings.TrimSpace(req.OfferingID)
	}
	if strings.TrimSpace(req.InteractionMode) != "" {
		current.InteractionMode = strings.TrimSpace(req.InteractionMode)
	}
	if req.WorkUnit.Name != "" {
		current.WorkUnit = req.WorkUnit
	}
	if req.Price.AmountWei != "" {
		current.Price = req.Price
	}
	if req.Extra != nil {
		current.Extra = req.Extra
	}
	if req.Constraints != nil {
		current.Constraints = req.Constraints
	}
	if strings.TrimSpace(req.Status) != "" {
		current.Status = types.OfferStatus(strings.TrimSpace(req.Status))
	}
	if current.CapabilityID == "" || current.OfferingID == "" || current.InteractionMode == "" {
		return types.Offer{}, fmt.Errorf("capability_id, offering_id, and interaction_mode are required")
	}
	if current.WorkUnit.Name == "" || len(current.WorkUnit.Extractor) == 0 {
		return types.Offer{}, fmt.Errorf("work_unit.name and work_unit.extractor are required")
	}
	if current.Price.AmountWei == "" || current.Price.PerUnits == 0 {
		return types.Offer{}, fmt.Errorf("price.amount_wei and price.per_units > 0 are required")
	}
	return current, nil
}

func assignmentFromRequest(req assignmentMutationRequest) (types.Assignment, error) {
	req.ID = strings.TrimSpace(req.ID)
	req.OfferID = strings.TrimSpace(req.OfferID)
	req.MemberBackendID = strings.TrimSpace(req.MemberBackendID)
	if req.ID == "" {
		return types.Assignment{}, fmt.Errorf("id is required")
	}
	if req.OfferID == "" || req.MemberBackendID == "" {
		return types.Assignment{}, fmt.Errorf("offer_id and member_backend_id are required")
	}
	status := types.AssignmentStatusActive
	if strings.TrimSpace(req.Status) != "" {
		status = types.AssignmentStatus(strings.TrimSpace(req.Status))
	}
	return types.Assignment{
		ID:              req.ID,
		OfferID:         req.OfferID,
		MemberBackendID: req.MemberBackendID,
		Status:          status,
		Notes:           req.Notes,
	}, nil
}

func validateJoinRequest(req types.JoinRequest) error {
	req.MemberEthAddress = strings.TrimSpace(req.MemberEthAddress)
	req.PayoutMode = strings.TrimSpace(req.PayoutMode)
	if req.MemberEthAddress == "" {
		return fmt.Errorf("member_eth_address is required")
	}
	if len(req.RequestedBackends) == 0 {
		return fmt.Errorf("requested_backends must contain at least one backend")
	}
	switch req.PayoutMode {
	case "", "onchain", "manual":
	default:
		return fmt.Errorf("payout_mode must be onchain or manual")
	}
	for i, backend := range req.RequestedBackends {
		if strings.TrimSpace(backend.ID) == "" {
			return fmt.Errorf("requested_backends[%d].id is required", i)
		}
		if strings.TrimSpace(backend.Transport) == "" {
			return fmt.Errorf("requested_backends[%d].transport is required", i)
		}
		if strings.TrimSpace(backend.URL) == "" {
			return fmt.Errorf("requested_backends[%d].url is required", i)
		}
	}
	return nil
}

func memberAndBackendsFromJoinRequest(req types.JoinRequest, now time.Time) (types.MemberRecord, []types.MemberBackend) {
	now = now.UTC()
	memberID := fmt.Sprintf("member-%d", now.UnixNano())
	payoutMode := req.PayoutMode
	if payoutMode == "" {
		payoutMode = "onchain"
	}
	member := types.MemberRecord{
		ID:                  memberID,
		EthAddress:          req.MemberEthAddress,
		DisplayName:         req.DisplayName,
		PayoutMode:          payoutMode,
		Status:              types.MemberStatusActive,
		SourceJoinRequestID: req.ID,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	backends := make([]types.MemberBackend, 0, len(req.RequestedBackends))
	for _, requested := range req.RequestedBackends {
		backends = append(backends, types.MemberBackend{
			ID:                  requested.ID,
			MemberID:            memberID,
			Transport:           requested.Transport,
			URL:                 requested.URL,
			Auth:                requested.Auth,
			HealthProbe:         requested.HealthProbe,
			ClaimedCapabilities: requested.ClaimedCapabilities,
			VerificationStatus:  requested.VerificationStatus,
			VerificationError:   requested.VerificationError,
			LastVerifiedAt:      requested.LastVerifiedAt,
			Status:              types.BackendStatusActive,
			CreatedAt:           now,
			UpdatedAt:           now,
		})
	}
	return member, backends
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

type backendOverrideRequest struct {
	MemberEthAddress string   `json:"member_eth_address"`
	BackendID        string   `json:"backend_id"`
	CapabilityID     string   `json:"capability_id"`
	OfferingID       string   `json:"offering_id"`
	Reason           string   `json:"reason,omitempty"`
	WarmupModifier   *float64 `json:"warmup_modifier,omitempty"`
	MaxShareCap      *float64 `json:"max_share_cap,omitempty"`
}

func validateBackendOutcome(outcome types.BackendOutcome) error {
	outcome.MemberEthAddress = strings.TrimSpace(outcome.MemberEthAddress)
	outcome.BackendID = strings.TrimSpace(outcome.BackendID)
	outcome.CapabilityID = strings.TrimSpace(outcome.CapabilityID)
	outcome.OfferingID = strings.TrimSpace(outcome.OfferingID)
	outcome.Outcome = strings.TrimSpace(outcome.Outcome)
	if outcome.MemberEthAddress == "" || outcome.BackendID == "" || outcome.CapabilityID == "" || outcome.OfferingID == "" {
		return fmt.Errorf("member_eth_address, backend_id, capability_id, and offering_id are required")
	}
	switch outcome.Outcome {
	case types.BackendOutcomeSuccess,
		types.BackendOutcomeBackendFailure,
		types.BackendOutcomeCallerFailure,
		types.BackendOutcomePolicyTermination,
		types.BackendOutcomePaymentTermination:
	default:
		return fmt.Errorf("outcome must be one of success, backend_failure, caller_failure, policy_termination, payment_termination")
	}
	if outcome.OccurredAt != nil && outcome.OccurredAt.IsZero() {
		return fmt.Errorf("occurred_at must be omitted or set to a valid timestamp")
	}
	return nil
}

type memberView struct {
	ID          string              `json:"id"`
	EthAddress  string              `json:"eth_address"`
	DisplayName string              `json:"display_name,omitempty"`
	PayoutMode  string              `json:"payout_mode,omitempty"`
	Status      string              `json:"status,omitempty"`
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

type backendSelectionSummaryView struct {
	GeneratedAt       time.Time                          `json:"generated_at"`
	Total             backendSelectionBucketSummaryView  `json:"total"`
	ByState           map[string]int                     `json:"by_state"`
	ScoreDistribution map[string]int                     `json:"score_distribution,omitempty"`
	TrafficShare      []backendTrafficShareView          `json:"traffic_share,omitempty"`
	ByMember          []backendSelectionGroupSummaryView `json:"by_member"`
	ByOffering        []backendSelectionGroupSummaryView `json:"by_offering"`
	WorstOfferings    []backendSelectionGroupSummaryView `json:"worst_offerings"`
	TopDegraded       []backendSelectionEntrySummaryView `json:"top_degraded"`
	TopExcluded       []backendSelectionEntrySummaryView `json:"top_excluded"`
}

type backendSelectionGroupSummaryView struct {
	Key                           string                            `json:"key"`
	Label                         string                            `json:"label,omitempty"`
	Count                         int                               `json:"count"`
	ByState                       map[string]int                    `json:"by_state"`
	ScoreDistribution             map[string]int                    `json:"score_distribution,omitempty"`
	TopRoutingReasons             map[string]int                    `json:"top_routing_reasons,omitempty"`
	TopExclusionReasons           map[string]int                    `json:"top_exclusion_reasons,omitempty"`
	TrafficShare                  []backendTrafficShareView         `json:"traffic_share,omitempty"`
	AverageEffectiveScore         float64                           `json:"average_effective_selection_score"`
	AverageSyntheticConfidence    float64                           `json:"average_synthetic_confidence"`
	AverageRealSuccessScore       float64                           `json:"average_real_success_score"`
	AverageRealLatencyScore       float64                           `json:"average_real_latency_score"`
	AverageRecentOutcomeCount     float64                           `json:"average_recent_outcome_count"`
	AverageRecentRoutableOutcomes float64                           `json:"average_recent_routable_outcome_count"`
	AverageRecentBackendFailures  float64                           `json:"average_recent_backend_failure_count"`
	AverageRecentWindowAgeSeconds float64                           `json:"average_recent_window_age_seconds"`
	RecentWindowStartedAt         *time.Time                        `json:"recent_window_started_at,omitempty"`
	RecentWindowEndedAt           *time.Time                        `json:"recent_window_ended_at,omitempty"`
	States                        backendSelectionBucketSummaryView `json:"states"`
}

type backendSelectionBucketSummaryView struct {
	Eligible    int `json:"eligible"`
	Degraded    int `json:"degraded"`
	Excluded    int `json:"excluded"`
	Quarantined int `json:"quarantined"`
}

type backendSelectionEntrySummaryView struct {
	MemberEthAddress           string     `json:"member_eth_address"`
	BackendID                  string     `json:"backend_id"`
	CapabilityID               string     `json:"capability_id"`
	OfferingID                 string     `json:"offering_id"`
	State                      string     `json:"state"`
	ExclusionReason            string     `json:"exclusion_reason,omitempty"`
	RoutingReason              string     `json:"routing_reason,omitempty"`
	EffectiveSelectionScore    float64    `json:"effective_selection_score"`
	SyntheticConfidence        float64    `json:"synthetic_confidence"`
	RealSuccessScore           float64    `json:"real_success_score"`
	RealLatencyScore           float64    `json:"real_latency_score"`
	RecentOutcomeCount         int        `json:"recent_outcome_count"`
	RecentRoutableOutcomeCount int        `json:"recent_routable_outcome_count"`
	RecentBackendFailureCount  int        `json:"recent_backend_failure_count"`
	RecentWindowStartedAt      *time.Time `json:"recent_window_started_at,omitempty"`
	RecentWindowEndedAt        *time.Time `json:"recent_window_ended_at,omitempty"`
	RecentWindowAgeSeconds     float64    `json:"recent_window_age_seconds,omitempty"`
}

type backendTrafficShareView struct {
	MemberEthAddress           string  `json:"member_eth_address"`
	BackendID                  string  `json:"backend_id"`
	CapabilityID               string  `json:"capability_id"`
	OfferingID                 string  `json:"offering_id"`
	RecentRoutableOutcomeCount int     `json:"recent_routable_outcome_count"`
	RecentRoutableTrafficShare float64 `json:"recent_routable_traffic_share"`
}

type publicWorstOfferingView struct {
	Key                          string                            `json:"key"`
	Count                        int                               `json:"count"`
	States                       backendSelectionBucketSummaryView `json:"states"`
	TopRoutingReasons            map[string]int                    `json:"top_routing_reasons,omitempty"`
	TopExclusionReasons          map[string]int                    `json:"top_exclusion_reasons,omitempty"`
	AverageEffectiveScore        float64                           `json:"average_effective_selection_score"`
	AverageRecentBackendFailures float64                           `json:"average_recent_backend_failure_count"`
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

type offerMutationRequest struct {
	ID              string          `json:"id"`
	CapabilityID    string          `json:"capability_id"`
	OfferingID      string          `json:"offering_id"`
	InteractionMode string          `json:"interaction_mode"`
	WorkUnit        config.WorkUnit `json:"work_unit"`
	Price           config.Price    `json:"price"`
	Extra           map[string]any  `json:"extra,omitempty"`
	Constraints     map[string]any  `json:"constraints,omitempty"`
	Status          string          `json:"status,omitempty"`
}

type assignmentMutationRequest struct {
	ID              string `json:"id"`
	OfferID         string `json:"offer_id"`
	MemberBackendID string `json:"member_backend_id"`
	Notes           string `json:"notes,omitempty"`
	Status          string `json:"status,omitempty"`
}

type memberStatusRequest struct {
	Status string `json:"status"`
}

type backendStatusRequest struct {
	Status string `json:"status"`
}

type joinRequestReviewRequest struct {
	Reason string `json:"reason,omitempty"`
}

func buildMemberViewsFromState(stateRepo *repo.StateRepo) ([]memberView, error) {
	if stateRepo == nil {
		return nil, nil
	}
	members, err := stateRepo.ListMembers()
	if err != nil {
		return nil, err
	}
	backends, err := stateRepo.ListMemberBackends()
	if err != nil {
		return nil, err
	}
	assignments, err := stateRepo.ListAssignments()
	if err != nil {
		return nil, err
	}
	offers, err := stateRepo.ListOffers()
	if err != nil {
		return nil, err
	}
	offersByID := make(map[string]types.Offer, len(offers))
	for _, offer := range offers {
		offersByID[offer.ID] = offer
	}
	assignmentsByBackend := make(map[string][]types.Assignment)
	for _, assignment := range assignments {
		assignmentsByBackend[assignment.MemberBackendID] = append(assignmentsByBackend[assignment.MemberBackendID], assignment)
	}
	backendsByMember := make(map[string][]types.MemberBackend)
	for _, backend := range backends {
		backendsByMember[backend.MemberID] = append(backendsByMember[backend.MemberID], backend)
	}

	out := make([]memberView, 0, len(members))
	for _, member := range members {
		view := memberView{
			ID:          member.ID,
			EthAddress:  member.EthAddress,
			DisplayName: member.DisplayName,
			PayoutMode:  member.PayoutMode,
			Status:      string(member.Status),
			Backends:    make([]memberBackendView, 0, len(backendsByMember[member.ID])),
		}
		memberBackends := backendsByMember[member.ID]
		sort.Slice(memberBackends, func(i, j int) bool { return memberBackends[i].ID < memberBackends[j].ID })
		for _, backend := range memberBackends {
			backendView := memberBackendView{
				ID:        backend.ID,
				Transport: backend.Transport,
				URL:       backend.URL,
				Offerings: make([]memberOfferingView, 0, len(assignmentsByBackend[backend.ID])),
			}
			if backend.Auth.Method != "" && backend.Auth.Method != "none" {
				backendView.Auth.Method = backend.Auth.Method
				backendView.Auth.SecretRefSet = backend.Auth.SecretRef != ""
			}
			backendAssignments := assignmentsByBackend[backend.ID]
			sort.Slice(backendAssignments, func(i, j int) bool { return backendAssignments[i].ID < backendAssignments[j].ID })
			for _, assignment := range backendAssignments {
				offer, ok := offersByID[assignment.OfferID]
				if !ok {
					continue
				}
				backendView.Offerings = append(backendView.Offerings, memberOfferingView{
					CapabilityID:    offer.CapabilityID,
					OfferingID:      offer.OfferingID,
					InteractionMode: offer.InteractionMode,
				})
			}
			view.Backends = append(view.Backends, backendView)
		}
		out = append(out, view)
	}
	return out, nil
}

func buildOfferingViewsFromState(stateRepo *repo.StateRepo) ([]offeringView, error) {
	if stateRepo == nil {
		return nil, nil
	}
	offers, err := stateRepo.ListOffers()
	if err != nil {
		return nil, err
	}
	members, err := stateRepo.ListMembers()
	if err != nil {
		return nil, err
	}
	backends, err := stateRepo.ListMemberBackends()
	if err != nil {
		return nil, err
	}
	assignments, err := stateRepo.ListAssignments()
	if err != nil {
		return nil, err
	}
	membersByID := make(map[string]types.MemberRecord, len(members))
	for _, member := range members {
		membersByID[member.ID] = member
	}
	backendsByID := make(map[string]types.MemberBackend, len(backends))
	for _, backend := range backends {
		backendsByID[backend.ID] = backend
	}
	assignmentsByOffer := make(map[string][]types.Assignment)
	for _, assignment := range assignments {
		assignmentsByOffer[assignment.OfferID] = append(assignmentsByOffer[assignment.OfferID], assignment)
	}

	out := make([]offeringView, 0, len(offers))
	for _, offer := range offers {
		view := offeringView{
			CapabilityID:    offer.CapabilityID,
			OfferingID:      offer.OfferingID,
			InteractionMode: offer.InteractionMode,
			Backends:        make([]offeringBackendView, 0, len(assignmentsByOffer[offer.ID])),
		}
		offerAssignments := assignmentsByOffer[offer.ID]
		sort.Slice(offerAssignments, func(i, j int) bool { return offerAssignments[i].ID < offerAssignments[j].ID })
		for _, assignment := range offerAssignments {
			backend, ok := backendsByID[assignment.MemberBackendID]
			if !ok {
				continue
			}
			member := membersByID[backend.MemberID]
			view.Backends = append(view.Backends, offeringBackendView{
				MemberEthAddress:  member.EthAddress,
				MemberDisplayName: member.DisplayName,
				BackendID:         backend.ID,
				Transport:         backend.Transport,
				URL:               backend.URL,
			})
		}
		view.BackendCount = len(view.Backends)
		out = append(out, view)
	}
	return out, nil
}

func buildBackendSelectionSummary(items []types.BackendSelectionState, scoring config.Scoring) backendSelectionSummaryView {
	summary := backendSelectionSummaryView{
		GeneratedAt:       time.Now().UTC(),
		ByState:           map[string]int{},
		ScoreDistribution: map[string]int{},
		TrafficShare:      make([]backendTrafficShareView, 0),
		ByMember:          make([]backendSelectionGroupSummaryView, 0),
		ByOffering:        make([]backendSelectionGroupSummaryView, 0),
		WorstOfferings:    make([]backendSelectionGroupSummaryView, 0),
		TopDegraded:       make([]backendSelectionEntrySummaryView, 0),
		TopExcluded:       make([]backendSelectionEntrySummaryView, 0),
	}
	memberGroups := map[string][]types.BackendSelectionState{}
	offeringGroups := map[string][]types.BackendSelectionState{}
	for _, item := range items {
		summary.Total = accumulateBackendSelectionBucket(summary.Total, item.State)
		summary.ByState[item.State]++
		summary.ScoreDistribution[scoreDistributionBucket(item.EffectiveSelectionScore)]++
		memberGroups[item.MemberEthAddress] = append(memberGroups[item.MemberEthAddress], item)
		offeringKey := item.CapabilityID + "/" + item.OfferingID
		offeringGroups[offeringKey] = append(offeringGroups[offeringKey], item)
		if item.RecentRoutableOutcomeCount > 0 {
			summary.TrafficShare = append(summary.TrafficShare, summarizeBackendTrafficShare(item, items))
		}
		switch item.State {
		case types.BackendSelectionStateDegraded:
			summary.TopDegraded = append(summary.TopDegraded, summarizeBackendSelectionEntry(item))
		case types.BackendSelectionStateExcluded, types.BackendSelectionStateQuarantined:
			summary.TopExcluded = append(summary.TopExcluded, summarizeBackendSelectionEntry(item))
		}
	}
	for member, group := range memberGroups {
		summary.ByMember = append(summary.ByMember, buildBackendSelectionGroupSummary(member, member, group))
	}
	for offering, group := range offeringGroups {
		summary.ByOffering = append(summary.ByOffering, buildBackendSelectionGroupSummary(offering, offering, group))
	}
	sort.Slice(summary.ByMember, func(i, j int) bool { return summary.ByMember[i].Key < summary.ByMember[j].Key })
	sort.Slice(summary.ByOffering, func(i, j int) bool { return summary.ByOffering[i].Key < summary.ByOffering[j].Key })
	for _, offering := range summary.ByOffering {
		if offering.States.Degraded == 0 && offering.States.Excluded == 0 && offering.States.Quarantined == 0 {
			continue
		}
		summary.WorstOfferings = append(summary.WorstOfferings, offering)
	}
	sort.Slice(summary.WorstOfferings, func(i, j int) bool {
		left := backendSelectionGroupSeverity(summary.WorstOfferings[i])
		right := backendSelectionGroupSeverity(summary.WorstOfferings[j])
		if left != right {
			return left > right
		}
		if summary.WorstOfferings[i].AverageEffectiveScore != summary.WorstOfferings[j].AverageEffectiveScore {
			return summary.WorstOfferings[i].AverageEffectiveScore < summary.WorstOfferings[j].AverageEffectiveScore
		}
		return summary.WorstOfferings[i].Key < summary.WorstOfferings[j].Key
	})
	sort.Slice(summary.TopDegraded, func(i, j int) bool {
		if summary.TopDegraded[i].EffectiveSelectionScore != summary.TopDegraded[j].EffectiveSelectionScore {
			return summary.TopDegraded[i].EffectiveSelectionScore < summary.TopDegraded[j].EffectiveSelectionScore
		}
		return summary.TopDegraded[i].RecentBackendFailureCount > summary.TopDegraded[j].RecentBackendFailureCount
	})
	sort.Slice(summary.TopExcluded, func(i, j int) bool {
		if summary.TopExcluded[i].RecentBackendFailureCount != summary.TopExcluded[j].RecentBackendFailureCount {
			return summary.TopExcluded[i].RecentBackendFailureCount > summary.TopExcluded[j].RecentBackendFailureCount
		}
		return summary.TopExcluded[i].EffectiveSelectionScore < summary.TopExcluded[j].EffectiveSelectionScore
	})
	sort.Slice(summary.TrafficShare, func(i, j int) bool {
		if summary.TrafficShare[i].RecentRoutableTrafficShare != summary.TrafficShare[j].RecentRoutableTrafficShare {
			return summary.TrafficShare[i].RecentRoutableTrafficShare > summary.TrafficShare[j].RecentRoutableTrafficShare
		}
		return summary.TrafficShare[i].BackendID < summary.TrafficShare[j].BackendID
	})
	if scoring.TopDegradedLimit > 0 && len(summary.TopDegraded) > scoring.TopDegradedLimit {
		summary.TopDegraded = summary.TopDegraded[:scoring.TopDegradedLimit]
	}
	if scoring.TopExcludedLimit > 0 && len(summary.TopExcluded) > scoring.TopExcludedLimit {
		summary.TopExcluded = summary.TopExcluded[:scoring.TopExcludedLimit]
	}
	if scoring.WorstOfferingsLimit > 0 && len(summary.WorstOfferings) > scoring.WorstOfferingsLimit {
		summary.WorstOfferings = summary.WorstOfferings[:scoring.WorstOfferingsLimit]
	}
	return summary
}

func backendSelectionGroupSeverity(group backendSelectionGroupSummaryView) float64 {
	return (float64(group.States.Quarantined) * 400) +
		(float64(group.States.Excluded) * 300) +
		(float64(group.States.Degraded) * 100) +
		(group.AverageRecentBackendFailures * 10) +
		((1 - group.AverageEffectiveScore) * 10)
}

func buildBackendSelectionGroupSummary(key, label string, items []types.BackendSelectionState) backendSelectionGroupSummaryView {
	out := backendSelectionGroupSummaryView{
		Key:                 key,
		Label:               label,
		Count:               len(items),
		ByState:             map[string]int{},
		ScoreDistribution:   map[string]int{},
		TopRoutingReasons:   map[string]int{},
		TopExclusionReasons: map[string]int{},
		TrafficShare:        make([]backendTrafficShareView, 0),
	}
	for _, item := range items {
		out.ByState[item.State]++
		out.ScoreDistribution[scoreDistributionBucket(item.EffectiveSelectionScore)]++
		out.States = accumulateBackendSelectionBucket(out.States, item.State)
		if item.RoutingReason != "" {
			out.TopRoutingReasons[item.RoutingReason]++
		}
		if item.ExclusionReason != "" {
			out.TopExclusionReasons[item.ExclusionReason]++
		}
		out.AverageEffectiveScore += item.EffectiveSelectionScore
		out.AverageSyntheticConfidence += item.SyntheticConfidence
		out.AverageRealSuccessScore += item.RealSuccessScore
		out.AverageRealLatencyScore += item.RealLatencyScore
		out.AverageRecentOutcomeCount += float64(item.RecentOutcomeCount)
		out.AverageRecentRoutableOutcomes += float64(item.RecentRoutableOutcomeCount)
		out.AverageRecentBackendFailures += float64(item.RecentBackendFailureCount)
		out.AverageRecentWindowAgeSeconds += item.RecentWindowAgeSeconds
		if item.RecentRoutableOutcomeCount > 0 {
			out.TrafficShare = append(out.TrafficShare, summarizeBackendTrafficShare(item, items))
		}
		if item.RecentWindowStartedAt != nil {
			if out.RecentWindowStartedAt == nil || item.RecentWindowStartedAt.Before(*out.RecentWindowStartedAt) {
				start := item.RecentWindowStartedAt.UTC()
				out.RecentWindowStartedAt = &start
			}
		}
		if item.RecentWindowEndedAt != nil {
			if out.RecentWindowEndedAt == nil || item.RecentWindowEndedAt.After(*out.RecentWindowEndedAt) {
				end := item.RecentWindowEndedAt.UTC()
				out.RecentWindowEndedAt = &end
			}
		}
	}
	if len(items) > 0 {
		divisor := float64(len(items))
		out.AverageEffectiveScore /= divisor
		out.AverageSyntheticConfidence /= divisor
		out.AverageRealSuccessScore /= divisor
		out.AverageRealLatencyScore /= divisor
		out.AverageRecentOutcomeCount /= divisor
		out.AverageRecentRoutableOutcomes /= divisor
		out.AverageRecentBackendFailures /= divisor
		out.AverageRecentWindowAgeSeconds /= divisor
	}
	sort.Slice(out.TrafficShare, func(i, j int) bool {
		if out.TrafficShare[i].RecentRoutableTrafficShare != out.TrafficShare[j].RecentRoutableTrafficShare {
			return out.TrafficShare[i].RecentRoutableTrafficShare > out.TrafficShare[j].RecentRoutableTrafficShare
		}
		return out.TrafficShare[i].BackendID < out.TrafficShare[j].BackendID
	})
	if len(out.TopRoutingReasons) == 0 {
		out.TopRoutingReasons = nil
	}
	if len(out.TopExclusionReasons) == 0 {
		out.TopExclusionReasons = nil
	}
	if len(out.ScoreDistribution) == 0 {
		out.ScoreDistribution = nil
	}
	if len(out.TrafficShare) == 0 {
		out.TrafficShare = nil
	}
	return out
}

func summarizeBackendSelectionEntry(item types.BackendSelectionState) backendSelectionEntrySummaryView {
	return backendSelectionEntrySummaryView{
		MemberEthAddress:           item.MemberEthAddress,
		BackendID:                  item.BackendID,
		CapabilityID:               item.CapabilityID,
		OfferingID:                 item.OfferingID,
		State:                      item.State,
		ExclusionReason:            item.ExclusionReason,
		RoutingReason:              item.RoutingReason,
		EffectiveSelectionScore:    item.EffectiveSelectionScore,
		SyntheticConfidence:        item.SyntheticConfidence,
		RealSuccessScore:           item.RealSuccessScore,
		RealLatencyScore:           item.RealLatencyScore,
		RecentOutcomeCount:         item.RecentOutcomeCount,
		RecentRoutableOutcomeCount: item.RecentRoutableOutcomeCount,
		RecentBackendFailureCount:  item.RecentBackendFailureCount,
		RecentWindowStartedAt:      item.RecentWindowStartedAt,
		RecentWindowEndedAt:        item.RecentWindowEndedAt,
		RecentWindowAgeSeconds:     item.RecentWindowAgeSeconds,
	}
}

func summarizeBackendTrafficShare(item types.BackendSelectionState, scope []types.BackendSelectionState) backendTrafficShareView {
	total := 0
	for _, candidate := range scope {
		total += candidate.RecentRoutableOutcomeCount
	}
	share := 0.0
	if total > 0 {
		share = float64(item.RecentRoutableOutcomeCount) / float64(total)
	}
	return backendTrafficShareView{
		MemberEthAddress:           item.MemberEthAddress,
		BackendID:                  item.BackendID,
		CapabilityID:               item.CapabilityID,
		OfferingID:                 item.OfferingID,
		RecentRoutableOutcomeCount: item.RecentRoutableOutcomeCount,
		RecentRoutableTrafficShare: share,
	}
}

func scoreDistributionBucket(score float64) string {
	switch {
	case score < 0.10:
		return "lt_0_10"
	case score < 0.30:
		return "0_10_to_0_29"
	case score < 0.50:
		return "0_30_to_0_49"
	case score < 0.80:
		return "0_50_to_0_79"
	default:
		return "0_80_to_1_00"
	}
}

func buildPublicWorstOfferingViews(items []backendSelectionGroupSummaryView, limit int) []publicWorstOfferingView {
	if len(items) == 0 {
		return nil
	}
	out := make([]publicWorstOfferingView, 0, len(items))
	for _, item := range items {
		out = append(out, publicWorstOfferingView{
			Key:                          item.Key,
			Count:                        item.Count,
			States:                       item.States,
			TopRoutingReasons:            item.TopRoutingReasons,
			TopExclusionReasons:          item.TopExclusionReasons,
			AverageEffectiveScore:        item.AverageEffectiveScore,
			AverageRecentBackendFailures: item.AverageRecentBackendFailures,
		})
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func accumulateBackendSelectionBucket(bucket backendSelectionBucketSummaryView, state string) backendSelectionBucketSummaryView {
	switch state {
	case types.BackendSelectionStateEligible:
		bucket.Eligible++
	case types.BackendSelectionStateDegraded:
		bucket.Degraded++
	case types.BackendSelectionStateExcluded:
		bucket.Excluded++
	case types.BackendSelectionStateQuarantined:
		bucket.Quarantined++
	}
	return bucket
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
	_, _ = fmt.Fprintln(w, "usage: livepeer-pool-controller <serve|version|generate-broker-config|import-legacy-config> [flags]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "normal production commands:")
	_, _ = fmt.Fprintln(w, "  serve")
	_, _ = fmt.Fprintln(w, "  version")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "migration-only compatibility commands:")
	_, _ = fmt.Fprintln(w, "  generate-broker-config")
	_, _ = fmt.Fprintln(w, "  import-legacy-config")
	return errors.New("invalid command")
}
