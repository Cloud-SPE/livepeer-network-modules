// Package scrape owns the broker-poll loop, the in-memory scrape
// cache, and the freshness/last-good-fallback bookkeeping per
// plan 0018 §5.
//
// One Service per coordinator process. The Service holds the cache
// state, runs the poll loop, and exposes a snapshot accessor for
// downstream consumers (candidate, roster, diff).
package scrape

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/providers/brokerclient"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

// Freshness flags applied per-broker after each scrape cycle.
const (
	FreshnessOK            = "ok"
	FreshnessStaleFailing  = "stale_failing"
	FreshnessSchemaError   = "schema_error"
	FreshnessNeverSucceeded = "never_succeeded"
)

// BrokerStatus holds the per-broker poll state held in the cache.
type BrokerStatus struct {
	Name           string
	BaseURL        string
	WorkerURL      string
	LastSuccessAt  time.Time
	LastAttemptAt  time.Time
	LastError      string
	Freshness      string
	Offerings      []types.BrokerOffering
}

// Snapshot is a point-in-time view of the scrape cache.
type Snapshot struct {
	OrchEthAddress string
	WindowStart    time.Time
	WindowEnd      time.Time
	Brokers        []BrokerStatus
	// SourceTuples is the flat list of (broker, offering) pairs that
	// the candidate service deduplicates by uniqueness key.
	SourceTuples []types.SourceTuple
}

// Config holds the scrape-loop tunables. Mirrors the flag surface in
// cmd/livepeer-orch-coordinator.
type Config struct {
	OrchEthAddress    string
	Brokers           []config.Broker
	ScrapeInterval    time.Duration
	ScrapeTimeout     time.Duration
	FreshnessWindow   time.Duration
	WorkerURLOverride map[string]string // broker name → worker_url; defaults to base_url
}

// Service runs the broker-poll loop.
type Service struct {
	cfg    Config
	client brokerclient.Client
	logger *slog.Logger

	mu        sync.RWMutex
	lastFetch time.Time
	cache     map[string]*BrokerStatus
}

// New builds a Service. Defaults are filled in for missing tunables.
func New(cfg Config, client brokerclient.Client, logger *slog.Logger) (*Service, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.ScrapeInterval <= 0 {
		cfg.ScrapeInterval = 30 * time.Second
	}
	if cfg.ScrapeTimeout <= 0 {
		cfg.ScrapeTimeout = 5 * time.Second
	}
	if cfg.FreshnessWindow <= 0 {
		cfg.FreshnessWindow = 5 * cfg.ScrapeInterval
	}
	if cfg.OrchEthAddress == "" {
		return nil, errors.New("scrape: orch eth address is required")
	}
	if len(cfg.Brokers) == 0 {
		return nil, errors.New("scrape: at least one broker is required")
	}
	s := &Service{
		cfg:    cfg,
		client: client,
		logger: logger,
		cache:  make(map[string]*BrokerStatus, len(cfg.Brokers)),
	}
	for _, b := range cfg.Brokers {
		workerURL := s.deriveWorkerURL(b)
		s.cache[b.Name] = &BrokerStatus{
			Name:      b.Name,
			BaseURL:   b.BaseURL,
			WorkerURL: workerURL,
			Freshness: FreshnessNeverSucceeded,
		}
	}
	return s, nil
}

// deriveWorkerURL chooses the worker_url the coordinator emits in the
// signed manifest for a given broker. Operators may override per-
// broker via cfg.WorkerURLOverride; otherwise the broker's base_url
// is used. The manifest schema requires HTTPS; production deployments
// MUST configure a public HTTPS-fronted URL via the override map.
func (s *Service) deriveWorkerURL(b config.Broker) string {
	if v, ok := s.cfg.WorkerURLOverride[b.Name]; ok && v != "" {
		return v
	}
	return b.BaseURL
}

// Run drives the poll loop until ctx is canceled. One synchronous
// scrape cycle on entry so the cache is warm before Run returns its
// first tick.
func (s *Service) Run(ctx context.Context) {
	s.scrapeOnce(ctx)
	t := time.NewTicker(s.cfg.ScrapeInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.scrapeOnce(ctx)
		}
	}
}

// ScrapeOnce runs a single cycle synchronously. Useful for tests and
// for the dev-mode bootstrap path.
func (s *Service) ScrapeOnce(ctx context.Context) {
	s.scrapeOnce(ctx)
}

func (s *Service) scrapeOnce(ctx context.Context) {
	windowStart := time.Now().UTC()
	for _, b := range s.cfg.Brokers {
		bctx, cancel := context.WithTimeout(ctx, s.cfg.ScrapeTimeout)
		offerings, err := s.client.Fetch(bctx, b.BaseURL)
		cancel()
		s.applyResult(b, offerings, err)
	}
	s.mu.Lock()
	s.lastFetch = time.Now().UTC()
	s.mu.Unlock()
	_ = windowStart
}

func (s *Service) applyResult(b config.Broker, offerings *types.BrokerOfferings, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.cache[b.Name]
	if !ok {
		return
	}
	now := time.Now().UTC()
	st.LastAttemptAt = now
	if err != nil {
		st.LastError = err.Error()
		switch {
		case errors.Is(err, brokerclient.ErrBrokerSchema):
			st.Freshness = FreshnessSchemaError
			st.Offerings = nil
			s.logger.Warn("broker scrape: schema-invalid; dropping entries",
				"broker", b.Name, "base_url", b.BaseURL, "err", err)
		case errors.Is(err, brokerclient.ErrBrokerUnreachable):
			if st.LastSuccessAt.IsZero() {
				st.Freshness = FreshnessNeverSucceeded
				st.Offerings = nil
			} else {
				st.Freshness = FreshnessStaleFailing
			}
			s.logger.Warn("broker scrape: soft failure; keeping last-good if any",
				"broker", b.Name, "base_url", b.BaseURL, "err", err)
		default:
			st.Freshness = FreshnessStaleFailing
			s.logger.Warn("broker scrape: unknown failure",
				"broker", b.Name, "base_url", b.BaseURL, "err", err)
		}
		return
	}

	if validateErr := offerings.Validate(s.cfg.OrchEthAddress); validateErr != nil {
		st.LastError = validateErr.Error()
		st.Freshness = FreshnessSchemaError
		st.Offerings = nil
		s.logger.Warn("broker scrape: validate failed; dropping entries",
			"broker", b.Name, "err", validateErr)
		return
	}
	st.LastError = ""
	st.LastSuccessAt = now
	st.Freshness = FreshnessOK
	st.Offerings = offerings.Capabilities
}

// Snapshot returns a deep-copy view of the cache. The window bounds
// are derived from the freshest broker's success timestamp; a broker
// without a recent success contributes its last-good entries flagged
// as stale.
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now().UTC()
	out := Snapshot{
		OrchEthAddress: s.cfg.OrchEthAddress,
		WindowEnd:      now,
		Brokers:        make([]BrokerStatus, 0, len(s.cache)),
	}
	earliest := now
	for _, b := range s.cfg.Brokers {
		st := s.cache[b.Name]
		if st == nil {
			continue
		}
		copyBroker := *st
		copyBroker.Offerings = append([]types.BrokerOffering(nil), st.Offerings...)
		out.Brokers = append(out.Brokers, copyBroker)
		if !st.LastSuccessAt.IsZero() && st.LastSuccessAt.Before(earliest) {
			earliest = st.LastSuccessAt
		}
		freshnessBound := now.Add(-s.cfg.FreshnessWindow)
		for _, o := range st.Offerings {
			if !st.LastSuccessAt.IsZero() && st.LastSuccessAt.Before(freshnessBound) && st.Freshness != FreshnessOK {
				continue
			}
			out.SourceTuples = append(out.SourceTuples, types.SourceTuple{
				BrokerName: st.Name,
				BaseURL:    st.BaseURL,
				WorkerURL:  st.WorkerURL,
				Offering:   o,
				ScrapedAt:  st.LastSuccessAt,
			})
		}
	}
	out.WindowStart = earliest
	return out
}

// IsValidWorkerURL is the boundary check the candidate service runs
// before emitting a tuple — a missing/invalid worker_url is the
// scrape-pipeline's responsibility to surface.
func IsValidWorkerURL(s string) bool {
	if s == "" {
		return false
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Host == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return true
	}
	return false
}
