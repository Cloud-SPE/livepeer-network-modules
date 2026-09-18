package repo

import (
	"fmt"
	"math"
	"strconv"
	"time"

	bolt "go.etcd.io/bbolt"
)

type RegionalWindowHold struct {
	StartRound uint64    `json:"start_round"`
	Reason     string    `json:"reason"`
	ObservedAt time.Time `json:"observed_at"`
}

// CloseReadyRegionalWindows advances every ended interval, retaining explicit
// holds for missing rounds. Later complete intervals can close independently.
func (r *StateRepo) CloseReadyRegionalWindows() error {
	terms, err := r.ListRegionalTerms()
	if err != nil {
		return err
	}
	if len(terms) == 0 {
		return nil
	}
	var highest uint64
	err = r.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("regional_round_numbers"))
		if b == nil {
			return nil
		}
		return b.ForEach(func(key, _ []byte) error {
			n, err := strconv.ParseUint(string(key), 10, 64)
			if err != nil {
				return err
			}
			if n > highest {
				highest = n
			}
			return nil
		})
	})
	if err != nil {
		return err
	}
	for i, term := range terms {
		if term.WindowRounds == 0 {
			return fmt.Errorf("invalid persisted regional window length")
		}
		limit := highest
		if i+1 < len(terms) && terms[i+1].EffectiveRound <= limit {
			limit = terms[i+1].EffectiveRound - 1
		}
		for start := term.EffectiveRound; start <= limit && term.WindowRounds-1 <= limit-start; {
			_, _, closeErr := r.CloseRegionalWindow(start)
			key := strconv.FormatUint(start, 10)
			if closeErr != nil {
				if err := putJSON(r, "regional_window_holds", key, RegionalWindowHold{StartRound: start, Reason: closeErr.Error(), ObservedAt: time.Now().UTC()}); err != nil {
					return err
				}
			} else if err := r.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("regional_window_holds")).Delete([]byte(key)) }); err != nil {
				return err
			}
			if start > math.MaxUint64-term.WindowRounds {
				break
			}
			start += term.WindowRounds
		}
	}
	return nil
}

func (r *StateRepo) RegionalWindowHolds() ([]RegionalWindowHold, error) {
	return listJSON(r, "regional_window_holds", func(a, b RegionalWindowHold) bool { return a.StartRound < b.StartRound })
}
