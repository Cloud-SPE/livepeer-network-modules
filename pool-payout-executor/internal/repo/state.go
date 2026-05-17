package repo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	runsBucket    = "runs"
	intentsBucket = "intents"
)

type StateRepo struct {
	db              *bolt.DB
	runHistoryLimit int
}

type RunRecord struct {
	RunID                string            `json:"run_id"`
	StartedAt            time.Time         `json:"started_at"`
	CompletedAt          time.Time         `json:"completed_at"`
	DryRun               bool              `json:"dry_run"`
	Error                string            `json:"error,omitempty"`
	ConfirmStatusCounts  map[string]uint64 `json:"confirm_status_counts,omitempty"`
	RequeueStatusCounts  map[string]uint64 `json:"requeue_status_counts,omitempty"`
	DispatchStatusCounts map[string]uint64 `json:"dispatch_status_counts,omitempty"`
}

type IntentRecord struct {
	IntentID         string    `json:"intent_id"`
	LastPhase        string    `json:"last_phase,omitempty"`
	LastStatus       string    `json:"last_status,omitempty"`
	DispatchAttempts uint64    `json:"dispatch_attempts"`
	ConfirmChecks    uint64    `json:"confirm_checks"`
	FailureCount     uint64    `json:"failure_count"`
	LastError        string    `json:"last_error,omitempty"`
	LastTxHash       string    `json:"last_tx_hash,omitempty"`
	FirstSeenAt      time.Time `json:"first_seen_at"`
	LastSeenAt       time.Time `json:"last_seen_at"`
	LastSucceededAt  time.Time `json:"last_succeeded_at,omitempty"`
	LastFailedAt     time.Time `json:"last_failed_at,omitempty"`
}

type IntentUpdate struct {
	IntentID        string
	Phase           string
	Status          string
	Error           string
	TxHash          string
	DispatchAttempt bool
	ConfirmCheck    bool
	Succeeded       bool
	Failed          bool
}

func Open(path string, runHistoryLimit int) (*StateRepo, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir state dir: %w", err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open state db: %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(runsBucket)); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists([]byte(intentsBucket))
		return err
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init state db: %w", err)
	}
	return &StateRepo{db: db, runHistoryLimit: runHistoryLimit}, nil
}

func (r *StateRepo) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

func (r *StateRepo) SaveRun(rec RunRecord) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(runsBucket))
		raw, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(rec.RunID), raw); err != nil {
			return err
		}
		if r.runHistoryLimit <= 0 {
			return nil
		}
		return pruneOldestRuns(b, r.runHistoryLimit)
	})
}

func (r *StateRepo) UpsertIntent(update IntentUpdate, at time.Time) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(intentsBucket))
		key := []byte(update.IntentID)
		var rec IntentRecord
		if raw := b.Get(key); raw != nil {
			if err := json.Unmarshal(raw, &rec); err != nil {
				return err
			}
		}
		if rec.IntentID == "" {
			rec.IntentID = update.IntentID
			rec.FirstSeenAt = at
		}
		rec.LastSeenAt = at
		rec.LastPhase = update.Phase
		rec.LastStatus = update.Status
		if update.DispatchAttempt {
			rec.DispatchAttempts++
		}
		if update.ConfirmCheck {
			rec.ConfirmChecks++
		}
		if update.Error != "" {
			rec.LastError = update.Error
		}
		if update.TxHash != "" {
			rec.LastTxHash = update.TxHash
		}
		if update.Succeeded {
			rec.LastSucceededAt = at
			rec.LastError = ""
		}
		if update.Failed {
			rec.FailureCount++
			rec.LastFailedAt = at
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		return b.Put(key, raw)
	})
}

func (r *StateRepo) GetIntent(intentID string) (IntentRecord, bool, error) {
	var out IntentRecord
	found := false
	err := r.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(intentsBucket)).Get([]byte(intentID))
		if raw == nil {
			return nil
		}
		found = true
		return json.Unmarshal(raw, &out)
	})
	if err != nil {
		return IntentRecord{}, false, fmt.Errorf("get intent: %w", err)
	}
	return out, found, nil
}

func (r *StateRepo) ListRuns(limit int) ([]RunRecord, error) {
	out := []RunRecord{}
	err := r.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(runsBucket)).Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			var rec RunRecord
			if err := json.Unmarshal(v, &rec); err != nil {
				return err
			}
			out = append(out, rec)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	return out, nil
}

func (r *StateRepo) ListIntents(limit int) ([]IntentRecord, error) {
	out := []IntentRecord{}
	err := r.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(intentsBucket)).Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var rec IntentRecord
			if err := json.Unmarshal(v, &rec); err != nil {
				return err
			}
			out = append(out, rec)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list intents: %w", err)
	}
	return out, nil
}

func pruneOldestRuns(b *bolt.Bucket, limit int) error {
	keys := make([][]byte, 0)
	c := b.Cursor()
	for k, _ := c.First(); k != nil; k, _ = c.Next() {
		key := make([]byte, len(k))
		copy(key, k)
		keys = append(keys, key)
	}
	for len(keys) > limit {
		k := keys[0]
		keys = keys[1:]
		if k == nil {
			return nil
		}
		if err := b.Delete(k); err != nil {
			return err
		}
	}
	return nil
}
