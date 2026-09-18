package repo

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	bolt "go.etcd.io/bbolt"
)

const revenueSourcesBucket = "regional_revenue_sources"

type SourceTransition struct {
	Action string    `json:"action"`
	Actor  string    `json:"actor"`
	Reason string    `json:"reason"`
	Round  int64     `json:"round"`
	At     time.Time `json:"at"`
}

type RevenueSource struct {
	RetirementProof    *revenue.DrainReport `json:"retirement_proof,omitempty"`
	History            []SourceTransition   `json:"history"`
	Source             revenue.Source       `json:"source"`
	StartRound         int64                `json:"start_round"`
	StopAcceptingRound *int64               `json:"stop_accepting_round,omitempty"`
	RetiredAfterRound  *int64               `json:"retired_after_round,omitempty"`
	State              string               `json:"state"`
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`
	AuditReason        string               `json:"audit_reason"`
}

func (r *StateRepo) RegisterRevenueSource(source revenue.Source, start int64, reason string, actors ...string) error {
	if err := source.Validate(); err != nil {
		return err
	}
	if source.PoolID != r.PoolID() || source.SourceID == "" || source.BrokerID == "" || source.ChainID == 0 || source.Payee == "" || source.URL == "" || start < 0 || reason == "" {
		return fmt.Errorf("invalid regional revenue source")
	}
	source.TokenFile = ""
	source.CAFile = ""
	return r.db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(revenueSourcesBucket))
		if err != nil {
			return err
		}
		if raw := bucket.Get([]byte(source.SourceID)); raw != nil {
			var previous RevenueSource
			if err := json.Unmarshal(raw, &previous); err != nil {
				return err
			}
			if previous.Source != source || previous.StartRound != start {
				return fmt.Errorf("registered source identity/participation cannot change")
			}
			return nil
		}
		if rounds := tx.Bucket([]byte("regional_round_numbers")); rounds != nil {
			if err := rounds.ForEach(func(key, _ []byte) error {
				round, err := strconv.ParseInt(string(key), 10, 64)
				if err != nil {
					return err
				}
				if round >= start {
					return fmt.Errorf("new source cannot rewrite a closed round")
				}
				return nil
			}); err != nil {
				return err
			}
		}
		// One broker resource cannot masquerade as multiple source ledgers.
		if err := bucket.ForEach(func(_, raw []byte) error {
			var previous RevenueSource
			if err := json.Unmarshal(raw, &previous); err != nil {
				return err
			}
			if previous.Source.BrokerID == source.BrokerID {
				return fmt.Errorf("broker resource already has a registered receiver source")
			}
			return nil
		}); err != nil {
			return err
		}
		now := time.Now().UTC()
		raw, err := json.Marshal(RevenueSource{History: []SourceTransition{{Action: "register", Actor: sourceActor(actors), Reason: reason, Round: start, At: now}}, Source: source, StartRound: start, State: "active", CreatedAt: now, UpdatedAt: now, AuditReason: reason})
		if err != nil {
			return err
		}
		return bucket.Put([]byte(source.SourceID), raw)
	})
}

// Draining preserves the source in every later accounting round until explicit
// settled retirement. Removing it from a process config never changes history.
func (r *StateRepo) DrainRevenueSource(id string, round int64, reason string, actors ...string) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(revenueSourcesBucket))
		if bucket == nil {
			return fmt.Errorf("source not found")
		}
		var record RevenueSource
		if err := json.Unmarshal(bucket.Get([]byte(id)), &record); err != nil {
			return fmt.Errorf("source not found")
		}
		if reason == "" || round < record.StartRound || record.State == "retired" {
			return fmt.Errorf("invalid drain transition")
		}
		if record.StopAcceptingRound != nil {
			if *record.StopAcceptingRound == round {
				return nil
			}
			return fmt.Errorf("source stop round is immutable")
		}
		record.StopAcceptingRound = &round
		record.State = "draining"
		record.UpdatedAt = time.Now().UTC()
		record.History = append(record.History, SourceTransition{Action: "drain", Actor: sourceActor(actors), Reason: reason, Round: round, At: record.UpdatedAt})
		raw, err := json.Marshal(record)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(id), raw)
	})
}

func (r *StateRepo) RevenueSources(round *int64) ([]RevenueSource, error) {
	result := []RevenueSource{}
	err := r.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(revenueSourcesBucket))
		if bucket == nil {
			return nil
		}
		return bucket.ForEach(func(_, raw []byte) error {
			var record RevenueSource
			if err := json.Unmarshal(raw, &record); err != nil {
				return err
			}
			if record.Source.PoolID != r.PoolID() {
				return fmt.Errorf("source belongs to another pool")
			}
			if round != nil && (record.StartRound > *round || record.RetiredAfterRound != nil && *record.RetiredAfterRound < *round) {
				return nil
			}
			result = append(result, record)
			return nil
		})
	})
	return result, err
}

func sourceActor(actors []string) string {
	if len(actors) == 1 && actors[0] != "" {
		return actors[0]
	}
	return "local-operator"
}

func (r *StateRepo) RetireRevenueSource(id string, after int64, reason string, proof revenue.DrainReport, actor string) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(revenueSourcesBucket))
		if bucket == nil {
			return fmt.Errorf("source not found")
		}
		var record RevenueSource
		if err := json.Unmarshal(bucket.Get([]byte(id)), &record); err != nil {
			return fmt.Errorf("source not found")
		}
		if record.RetiredAfterRound != nil {
			if *record.RetiredAfterRound == after {
				return nil
			}
			return fmt.Errorf("retirement round is immutable")
		}
		if reason == "" || actor == "" || record.State != "draining" || record.StopAcceptingRound == nil || after < *record.StopAcceptingRound {
			return fmt.Errorf("source must drain before retirement")
		}
		if err := proof.Validate(record.Source, after, time.Now()); err != nil {
			return err
		}
		index := tx.Bucket([]byte("regional_round_numbers"))
		if index == nil {
			return fmt.Errorf("source accounting rounds not closed")
		}
		var covered uint64
		if err := index.ForEach(func(key, id []byte) error {
			round, err := strconv.ParseInt(string(key), 10, 64)
			if err != nil {
				return err
			}
			if round < record.StartRound || round > after {
				return nil
			}
			var closed types.RoundReceipt
			if err := json.Unmarshal(tx.Bucket([]byte(roundReceiptsBucket)).Get(id), &closed); err != nil {
				return err
			}
			included := false
			for _, report := range closed.RevenueReports {
				if report.SourceID == record.Source.SourceID {
					included = true
					break
				}
			}
			if !included {
				return fmt.Errorf("closed round lacks retiring source")
			}
			covered++
			return nil
		}); err != nil {
			return err
		}
		if covered != uint64(after-record.StartRound)+1 {
			return fmt.Errorf("every source participation round must close before retirement")
		}
		now := time.Now().UTC()
		record.State = "retired"
		record.RetiredAfterRound = &after
		record.UpdatedAt = now
		record.RetirementProof = &proof
		record.History = append(record.History, SourceTransition{Action: "retire", Actor: actor, Reason: reason, Round: after, At: now})
		raw, err := json.Marshal(record)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(id), raw)
	})
}
