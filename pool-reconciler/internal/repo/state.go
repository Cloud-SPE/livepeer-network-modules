package repo

import (
	"encoding/json"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

const roundsBucket = "rounds"

type StateRepo struct {
	db *bolt.DB
}

type RoundRecord struct {
	RoundID       uint64    `json:"round_id"`
	Status        string    `json:"status"`
	Attempts      uint64    `json:"attempts"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	ClosedAt      time.Time `json:"closed_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

func Open(path string) (*StateRepo, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open state db: %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(roundsBucket))
		return err
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init state db: %w", err)
	}
	return &StateRepo{db: db}, nil
}

func (r *StateRepo) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

func (r *StateRepo) GetRound(roundID uint64) (RoundRecord, bool, error) {
	var out RoundRecord
	found := false
	err := r.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(roundsBucket)).Get([]byte(roundKey(roundID)))
		if raw == nil {
			return nil
		}
		found = true
		return json.Unmarshal(raw, &out)
	})
	if err != nil {
		return RoundRecord{}, false, fmt.Errorf("get round: %w", err)
	}
	return out, found, nil
}

func (r *StateRepo) ListPendingRounds(limit int) ([]RoundRecord, error) {
	records := []RoundRecord{}
	err := r.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(roundsBucket)).Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var rec RoundRecord
			if err := json.Unmarshal(v, &rec); err != nil {
				return err
			}
			if rec.Status == "closed" {
				continue
			}
			records = append(records, rec)
			if limit > 0 && len(records) >= limit {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list pending rounds: %w", err)
	}
	return records, nil
}

func (r *StateRepo) MarkAttempt(roundID uint64) error {
	return r.update(roundID, func(rec *RoundRecord) {
		rec.RoundID = roundID
		rec.Attempts++
		rec.LastAttemptAt = time.Now().UTC()
		if rec.Status == "" {
			rec.Status = "attempted"
		}
	})
}

func (r *StateRepo) MarkClosed(roundID uint64) error {
	return r.update(roundID, func(rec *RoundRecord) {
		rec.RoundID = roundID
		rec.Status = "closed"
		rec.ClosedAt = time.Now().UTC()
		rec.LastError = ""
	})
}

func (r *StateRepo) MarkFailed(roundID uint64, errMsg string) error {
	return r.update(roundID, func(rec *RoundRecord) {
		rec.RoundID = roundID
		rec.Status = "failed"
		rec.LastError = errMsg
	})
}

func (r *StateRepo) update(roundID uint64, mutate func(*RoundRecord)) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(roundsBucket))
		key := []byte(roundKey(roundID))
		var rec RoundRecord
		if raw := b.Get(key); raw != nil {
			if err := json.Unmarshal(raw, &rec); err != nil {
				return err
			}
		}
		mutate(&rec)
		raw, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		return b.Put(key, raw)
	})
}

func roundKey(roundID uint64) string {
	return fmt.Sprintf("%020d", roundID)
}
