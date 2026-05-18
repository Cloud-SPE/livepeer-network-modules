package repo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"
)

const snapshotsBucket = "snapshots"

type StateRepo struct {
	db *bolt.DB
}

type Snapshot struct {
	ID                 string    `json:"id"`
	CreatedAt          time.Time `json:"created_at"`
	Source             string    `json:"source"`
	MemberCount        int       `json:"member_count"`
	RenderedBytes      int       `json:"rendered_bytes"`
	ConfigYAML         string    `json:"config_yaml"`
	RenderedBrokerYAML string    `json:"rendered_broker_yaml"`
}

func Open(dir string) (*StateRepo, error) {
	if dir == "" {
		return nil, fmt.Errorf("data dir is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir data dir: %w", err)
	}
	db, err := bolt.Open(filepath.Join(dir, "pool-controller.db"), 0o600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open bolt db: %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(snapshotsBucket))
		if err != nil {
			return err
		}
		if err := (&StateRepo{db: db}).initControlPlaneBuckets(tx); err != nil {
			return err
		}
		if err := (&StateRepo{db: db}).initBackendSelectionBuckets(tx); err != nil {
			return err
		}
		return (&StateRepo{db: db}).initReceiptBuckets(tx)
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init bolt db: %w", err)
	}
	return &StateRepo{db: db}, nil
}

func (r *StateRepo) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

func (r *StateRepo) SaveSnapshot(s Snapshot) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("repo is not open")
	}
	if s.ID == "" {
		s.ID = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(snapshotsBucket)).Put([]byte(s.ID), raw)
	})
}

func (r *StateRepo) LatestSnapshot() (*Snapshot, error) {
	items, err := r.ListSnapshots(1)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}

func (r *StateRepo) ListSnapshots(limit int) ([]Snapshot, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("repo is not open")
	}
	out := make([]Snapshot, 0)
	err := r.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(snapshotsBucket)).Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			var snap Snapshot
			if err := json.Unmarshal(v, &snap); err != nil {
				return err
			}
			out = append(out, snap)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}
