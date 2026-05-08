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
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
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
	EthAddress     types.EthAddress
	ResolvedURI    string
	Mode           types.ResolveMode
	Manifest       *types.Manifest // nil for legacy mode
	LegacyURL      string          // set for legacy mode
	FetchedAt      time.Time
	ChainSeenAt    time.Time
	ManifestSHA256 [32]byte
	SchemaVersion  string
}

// Repo is the cache repository interface used by service/.
type Repo interface {
	Get(addr types.EthAddress) (*Entry, bool, error)
	Put(e *Entry) error
	Delete(addr types.EthAddress) error
	List() ([]types.EthAddress, error)
}

// store-backed implementation.
type repo struct {
	s store.Store
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
	return &e, true, nil
}

// Put writes the entry; ETags / version checks are not used (last write wins).
func (r *repo) Put(e *Entry) error {
	if e == nil {
		return fmt.Errorf("manifestcache: nil entry")
	}
	if e.EthAddress == "" {
		return fmt.Errorf("manifestcache: missing eth_address")
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(e); err != nil {
		return fmt.Errorf("manifestcache: encode: %w", err)
	}
	return r.s.Put(Bucket, key(e.EthAddress), buf.Bytes())
}

// Delete removes the entry.
func (r *repo) Delete(addr types.EthAddress) error {
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
