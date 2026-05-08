// Package audit holds the audit-event log used by service/resolver
// and service/publisher to surface what happened, when, for which
// address. Storage is a single bucket keyed by `(addr || rfc3339nano)`
// so range scans yield ordered events per address.
package audit

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

// WithMetrics wraps a Repo so each Append also bumps audit_events_total.
func WithMetrics(r Repo, rec metrics.Recorder) Repo {
	if rec == nil {
		return r
	}
	return &meteredRepo{inner: r, rec: rec}
}

type meteredRepo struct {
	inner Repo
	rec   metrics.Recorder
}

func (m *meteredRepo) Append(e types.AuditEvent) error {
	err := m.inner.Append(e)
	if err == nil {
		m.rec.IncAudit(e.Kind.String())
	}
	return err
}

func (m *meteredRepo) Query(addr types.EthAddress, since time.Time, limit int) ([]types.AuditEvent, error) {
	return m.inner.Query(addr, since, limit)
}

// Bucket is the BoltDB bucket name for audit-log entries.
var Bucket = []byte("audit_log")

// Repo is the audit-log repository.
type Repo interface {
	Append(types.AuditEvent) error
	Query(addr types.EthAddress, since time.Time, limit int) ([]types.AuditEvent, error)
}

type repo struct {
	s store.Store
}

// New returns a Repo backed by the given Store.
func New(s store.Store) Repo {
	return &repo{s: s}
}

// Append writes the event with At set to now if zero.
func (r *repo) Append(e types.AuditEvent) error {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	if e.EthAddress == "" {
		return fmt.Errorf("audit: missing eth_address")
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(e); err != nil {
		return fmt.Errorf("audit: encode: %w", err)
	}
	return r.s.Put(Bucket, eventKey(e.EthAddress, e.At), buf.Bytes())
}

// Query returns events for addr at or after `since`, capped at limit.
// Iteration order in Memory store is non-deterministic; in Bolt it's
// key-sorted (which equals time-sorted by construction).
func (r *repo) Query(addr types.EthAddress, since time.Time, limit int) ([]types.AuditEvent, error) {
	addr = types.EthAddress(strings.ToLower(string(addr)))
	prefix := []byte(string(addr) + "|")
	var out []types.AuditEvent
	err := r.s.ForEach(Bucket, func(k, v []byte) error {
		if !bytes.HasPrefix(k, prefix) {
			return nil
		}
		var e types.AuditEvent
		if err := gob.NewDecoder(bytes.NewReader(v)).Decode(&e); err != nil {
			return nil //nolint:nilerr // skip corrupt audit entries; one bad row shouldn't poison Query
		}
		if !since.IsZero() && e.At.Before(since) {
			return nil
		}
		out = append(out, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Sort by time ascending and apply limit.
	sortByTime(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func eventKey(addr types.EthAddress, at time.Time) []byte {
	return []byte(strings.ToLower(string(addr)) + "|" + at.UTC().Format(time.RFC3339Nano))
}

// sortByTime is a tiny insertion sort to avoid depending on sort
// package — events arrive few-at-a-time and the slices are small.
// Keeps utils zero-dep.
func sortByTime(s []types.AuditEvent) {
	for i := 1; i < len(s); i++ {
		j := i
		for j > 0 && s[j-1].At.After(s[j].At) {
			s[j-1], s[j] = s[j], s[j-1]
			j--
		}
	}
}
