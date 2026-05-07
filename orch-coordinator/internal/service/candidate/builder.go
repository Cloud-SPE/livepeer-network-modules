package candidate

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/candidates"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/scrape"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

// Observer is a metrics hook the builder calls after each Rebuild.
type Observer interface {
	ObserveCandidateBuild(outcome string, dur time.Duration)
}

// Builder owns the periodic candidate-build pass: pull a snapshot
// from the scrape service, fold into a candidate, save under the
// candidate-history store, expose a Latest() accessor.
type Builder struct {
	scrape   *scrape.Service
	store    *candidates.Store
	opts     BuildOptions
	logger   *slog.Logger
	observer Observer

	mu      sync.RWMutex
	latest  *types.Candidate
	lastErr error
}

// SetObserver attaches a metrics observer.
func (b *Builder) SetObserver(o Observer) { b.observer = o }

// NewBuilder wires the Builder. The opts.PublicationSeq is read
// fresh on each Build; callers wanting to advance it must mutate the
// builder via SetPublicationSeq.
func NewBuilder(scrapeSvc *scrape.Service, store *candidates.Store, opts BuildOptions, logger *slog.Logger) (*Builder, error) {
	if scrapeSvc == nil {
		return nil, errors.New("candidate.Builder: scrape service is required")
	}
	if store == nil {
		return nil, errors.New("candidate.Builder: candidate store is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if opts.ManifestTTL <= 0 {
		opts.ManifestTTL = 24 * time.Hour
	}
	return &Builder{scrape: scrapeSvc, store: store, opts: opts, logger: logger}, nil
}

// Rebuild reads the current scrape snapshot, builds a candidate, and
// saves it. Returns the new candidate (or the previous one on hard
// failure — the previous candidate stays the operator's reference
// point).
func (b *Builder) Rebuild() (*types.Candidate, error) {
	start := time.Now()
	snap := b.scrape.Snapshot()
	cand, err := Build(snap, b.opts)
	if err != nil {
		b.mu.Lock()
		b.lastErr = err
		b.mu.Unlock()
		b.emit("conflict", time.Since(start))
		return nil, err
	}
	metaBytes, err := MarshalMetadata(cand.Metadata)
	if err != nil {
		b.emit("error", time.Since(start))
		return nil, err
	}
	tarBytes, err := PackTarball(cand)
	if err != nil {
		b.emit("error", time.Since(start))
		return nil, err
	}
	if _, err := b.store.Save(candidates.Snapshot{
		Timestamp:     cand.Metadata.CandidateTimestamp,
		ManifestBytes: cand.ManifestBytes,
		MetadataBytes: metaBytes,
		TarballBytes:  tarBytes,
	}); err != nil {
		b.mu.Lock()
		b.lastErr = err
		b.mu.Unlock()
		b.emit("error", time.Since(start))
		return nil, err
	}
	b.mu.Lock()
	b.latest = cand
	b.lastErr = nil
	b.mu.Unlock()
	b.emit("ok", time.Since(start))
	return cand, nil
}

func (b *Builder) emit(outcome string, dur time.Duration) {
	if b.observer != nil {
		b.observer.ObserveCandidateBuild(outcome, dur)
	}
}

// Latest returns the most-recently-built candidate, or nil before
// the first successful build.
func (b *Builder) Latest() *types.Candidate {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.latest
}

// LastError reports the last build error, or nil on success.
func (b *Builder) LastError() error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.lastErr
}

// SetPublicationSeq updates the publication_seq used by future Build
// calls. The cold key on secure-orch decides the canonical seq; the
// coordinator's view is advisory.
func (b *Builder) SetPublicationSeq(seq uint64) {
	b.mu.Lock()
	b.opts.PublicationSeq = seq
	b.mu.Unlock()
}
