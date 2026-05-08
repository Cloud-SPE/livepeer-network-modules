// Package manifestfetcher holds the ManifestFetcher provider — HTTP
// retrieval of an off-chain manifest body with a hard size cap and
// per-request timeout. Used by service/resolver to pull the exact
// manifest URL returned on-chain.
package manifestfetcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

// WithMetrics wraps a ManifestFetcher so every fetch outcome is
// recorded. Bytes observed only on success.
func WithMetrics(f ManifestFetcher, rec metrics.Recorder) ManifestFetcher {
	if rec == nil {
		return f
	}
	return &meteredFetcher{inner: f, rec: rec}
}

type meteredFetcher struct {
	inner ManifestFetcher
	rec   metrics.Recorder
}

func (m *meteredFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	start := time.Now()
	body, err := m.inner.Fetch(ctx, url)
	dur := time.Since(start)
	outcome := classifyFetchErr(ctx, err)
	m.rec.IncManifestFetch(outcome)
	m.rec.ObserveManifestFetch(outcome, dur, len(body))
	if err == nil {
		m.rec.SetManifestFetcherLastSuccess(time.Now())
	}
	return body, err
}

// classifyFetchErr maps a fetcher error into a metric outcome label.
func classifyFetchErr(ctx context.Context, err error) string {
	switch {
	case err == nil:
		return metrics.OutcomeOK
	case errors.Is(err, types.ErrManifestTooLarge):
		return metrics.OutcomeTooLarge
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return metrics.OutcomeTimeout
	default:
		return metrics.OutcomeHTTPError
	}
}

// ManifestFetcher fetches a manifest body. Implementations must:
//   - apply MaxBytes as a hard limit (reject larger bodies).
//   - apply Timeout per request.
//   - return types.ErrManifestTooLarge for size exhaustion.
//   - return types.ErrManifestUnavailable for any other transport / HTTP failure.
type ManifestFetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// HTTP is the default implementation backed by net/http.
type HTTP struct {
	client   *http.Client
	maxBytes int64
}

// Config captures the construction parameters.
type Config struct {
	MaxBytes int64
	Timeout  time.Duration
	// AllowInsecure permits http:// URLs (only needed for dev mode).
	AllowInsecure bool
}

// New returns an HTTP ManifestFetcher.
func New(cfg Config) *HTTP {
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 4 * 1024 * 1024
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	cli := &http.Client{
		Timeout: cfg.Timeout,
		// We do not follow redirects across schemes — a manifest URL
		// must redirect within https only.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !cfg.AllowInsecure && req.URL.Scheme != "https" {
				return errors.New("manifest fetcher: redirect to non-https blocked")
			}
			if len(via) > 5 {
				return errors.New("manifest fetcher: too many redirects")
			}
			return nil
		},
	}
	return &HTTP{client: cli, maxBytes: cfg.MaxBytes}
}

// Fetch GETs the URL and returns the body up to MaxBytes.
func (f *HTTP) Fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %w", types.ErrManifestUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "livepeer-service-registry/0.1")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", types.ErrManifestUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d", types.ErrManifestUnavailable, resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(strings.ToLower(ct), "json") {
		// We don't strictly require JSON content-type, but a non-JSON
		// type is a strong hint the operator is misconfigured. Log via
		// returned error and proceed.
		// Note: returning the body anyway makes us tolerant; a future
		// iteration could be strict.
		_ = ct
	}

	limited := io.LimitReader(resp.Body, f.maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("%w: read: %w", types.ErrManifestUnavailable, err)
	}
	if int64(len(body)) > f.maxBytes {
		return nil, fmt.Errorf("%w: body > %d bytes", types.ErrManifestTooLarge, f.maxBytes)
	}
	return body, nil
}

// Static is a fetcher backed by a fixed map[url][]byte. Used by tests
// and the static-overlay-only example.
type Static struct {
	Bodies map[string][]byte
	// Errors maps URL to a fixed error to return; useful for failure tests.
	Errors map[string]error
}

// Fetch returns the configured body or error for url, or ErrNotFound-style.
func (s *Static) Fetch(_ context.Context, url string) ([]byte, error) {
	if s == nil {
		return nil, types.ErrManifestUnavailable
	}
	if e, ok := s.Errors[url]; ok && e != nil {
		return nil, e
	}
	body, ok := s.Bodies[url]
	if !ok {
		return nil, fmt.Errorf("%w: no static body for %s", types.ErrManifestUnavailable, url)
	}
	return body, nil
}
