// Package manifestcache holds the resolver's manifest cache. Reads
// and writes go through the providers/store interface, so the
// physical backing (BoltDB / in-memory / future Postgres) is a
// provider swap.
//
// Cache entries carry the parsed manifest plus resolver-side
// metadata (mode, on-chain URI seen, fetched timestamp). A miss is
// a non-error returning (nil, false). Schema for the encoded value
// is gob — internal-only, not cross-language.
package manifestcache

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

// WithMetrics wraps a Repo so List + Put + Delete outcomes drive the
// cache_entries gauge and cache_evictions_total counter. The
// resolver-level cache_lookups counter and cache_writes counter live
// in service/resolver because Repo.Get returns false on miss without
// distinguishing a "fresh hit" from a "stale hit" — that's resolver-
// territory.
func WithMetrics(r Repo, rec metrics.Recorder) Repo {
	if rec == nil {
		return r
	}
	mr := &meteredRepo{inner: r, rec: rec}
	mr.refreshGauge()
	return mr
}

type meteredRepo struct {
	inner Repo
	rec   metrics.Recorder
}

func (m *meteredRepo) Get(addr types.EthAddress) (*Entry, bool, error) {
	return m.inner.Get(addr)
}

func (m *meteredRepo) Put(e *Entry) error {
	err := m.inner.Put(e)
	if err == nil {
		m.refreshGauge()
	}
	return err
}

func (m *meteredRepo) Delete(addr types.EthAddress) error {
	err := m.inner.Delete(addr)
	if err == nil {
		m.rec.IncCacheEviction(metrics.EvictForced)
		m.refreshGauge()
	}
	return err
}

func (m *meteredRepo) List() ([]types.EthAddress, error) {
	return m.inner.List()
}

func (m *meteredRepo) refreshGauge() {
	if list, err := m.inner.List(); err == nil {
		m.rec.SetCacheEntries(len(list))
	}
}

// Bucket is the BoltDB bucket name for manifest-cache entries.
var Bucket = []byte("manifest_cache")

// Entry is one cache record. Field shape mirrors
// docs/design-docs/resolver-cache.md Entry struct.
type Entry struct {
	EthAddress         types.EthAddress
	OverlayManifestURL string // configured overlay pointer; empty for chain-derived entries
	ResolvedURI        string
	Mode               types.ResolveMode
	Manifest           *types.Manifest // nil for legacy mode
	LegacyURL          string          // set for legacy mode
	FetchedAt          time.Time
	ChainSeenAt        time.Time
	ManifestSHA256     [32]byte
	PublicationSeq     uint64
	SchemaVersion      string
}

// Repo is the cache repository interface used by service/.
type Repo interface {
	ListDiscovery() ([]types.EthAddress, error)
	GetDiscovery(types.EthAddress) (types.DiscoveryStatus, bool, error)
	PutDiscovery(types.EthAddress, types.DiscoveryStatus) error
	Get(addr types.EthAddress) (*Entry, bool, error)
	Put(e *Entry) error
	Delete(addr types.EthAddress) error
	List() ([]types.EthAddress, error)
}

// store-backed implementation.
type repo struct {
	s  store.Store
	mu sync.Mutex
}

// New returns a Repo backed by the given Store.
func New(s store.Store) Repo {
	return &repo{s: s}
}

// Get returns the cache entry for addr, or (nil, false, nil) on miss.
func (r *repo) Get(addr types.EthAddress) (*Entry, bool, error) {
	raw, err := r.s.Get(Bucket, key(addr))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("manifestcache: get: %w", err)
	}
	var e Entry
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&e); err != nil {
		return nil, false, fmt.Errorf("manifestcache: decode: %w", err)
	}
	if e.Manifest != nil {
		h, err := r.watermark(e.EthAddress)
		if err != nil {
			return nil, false, err
		}
		if err := h.check(&e); err != nil {
			return nil, false, err
		}
	}
	return &e, true, nil
}

// Put durably accepts signed publications before replacing cached data.
func (r *repo) Put(e *Entry) error {
	if e == nil {
		return fmt.Errorf("manifestcache: nil entry")
	}
	if e.EthAddress == "" {
		return fmt.Errorf("manifestcache: missing eth_address")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	h, err := r.watermark(e.EthAddress)
	if err != nil {
		return err
	}
	if !h.Set {
		old, ok, err := r.Get(e.EthAddress)
		if err != nil {
			return err
		}
		if ok && old.Manifest != nil {
			h = markFor(old)
		}
	}
	if e.Manifest != nil {
		if err := h.check(e); err != nil {
			return err
		}
		h = markFor(e)
	}
	if h.Set {
		if err := r.saveWatermark(e.EthAddress, h); err != nil {
			return err
		}
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(e); err != nil {
		return fmt.Errorf("manifestcache: encode: %w", err)
	}
	return r.s.Put(Bucket, key(e.EthAddress), buf.Bytes())
}

// Delete removes the entry.
func (r *repo) Delete(addr types.EthAddress) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, err := r.watermark(addr)
	if err != nil {
		return err
	}
	if !h.Set {
		old, ok, err := r.Get(addr)
		if err != nil {
			return err
		}
		if ok && old.Manifest != nil {
			if err := r.saveWatermark(addr, markFor(old)); err != nil {
				return err
			}
		}
	}
	return r.s.Delete(Bucket, key(addr))
}

// List returns all cached addresses.
func (r *repo) List() ([]types.EthAddress, error) {
	var out []types.EthAddress
	err := r.s.ForEach(Bucket, func(k, _ []byte) error {
		out = append(out, types.EthAddress(strings.ToLower(string(k))))
		return nil
	})
	return out, err
}

func key(addr types.EthAddress) []byte {
	return []byte(strings.ToLower(string(addr)))
}

// Watermarks outlive cache deletion and discovery source changes.
var watermarkBucket = []byte("manifest_publication_watermarks")

type publicationMark struct {
	Set       bool
	Seq       uint64
	Canonical [32]byte
	Raw       [32]byte
}

func markFor(e *Entry) publicationMark {
	return publicationMark{Set: true, Seq: e.PublicationSeq, Canonical: e.Manifest.CanonicalSHA256, Raw: e.ManifestSHA256}
}
func (h publicationMark) check(e *Entry) error {
	if !h.Set {
		return nil
	}
	conflict := e.PublicationSeq < h.Seq
	if e.PublicationSeq == h.Seq {
		if h.Canonical != ([32]byte{}) {
			conflict = e.Manifest.CanonicalSHA256 != h.Canonical
		} else {
			conflict = e.ManifestSHA256 != h.Raw
		}
	}
	if conflict {
		return types.NewValidation(errors.Join(types.ErrParse, types.ErrPublicationReplay), "manifest.publication_seq", "publication rollback or conflicting payload at an accepted sequence")
	}
	return nil
}
func (r *repo) watermark(addr types.EthAddress) (publicationMark, error) {
	var h publicationMark
	raw, err := r.s.Get(watermarkBucket, key(addr))
	if errors.Is(err, store.ErrNotFound) {
		return h, nil
	}
	if err != nil {
		return h, err
	}
	err = gob.NewDecoder(bytes.NewReader(raw)).Decode(&h)
	return h, err
}
func (r *repo) saveWatermark(addr types.EthAddress, h publicationMark) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(h); err != nil {
		return err
	}
	return r.s.Put(watermarkBucket, key(addr), buf.Bytes())
}
