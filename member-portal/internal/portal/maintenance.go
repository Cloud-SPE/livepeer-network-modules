package portal

import (
	"encoding/json"
	"errors"
	bolt "go.etcd.io/bbolt"
	"time"
)

// BindOrigin keeps a copied store from silently becoming another portal issuer.
// Domain migration requires an explicit fresh session store; regional pool IDs do not change.
func (s *Store) BindOrigin(origin string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("identity"))
		if err != nil {
			return err
		}
		old := b.Get([]byte("origin"))
		if old != nil && string(old) != origin {
			return errors.New("portal state belongs to another origin; invalidate sessions explicitly for domain migration")
		}
		return b.Put([]byte("origin"), []byte(origin))
	})
}

// Prune removes expired authentication material; consumed nonces are already deleted atomically.
func (s *Store) Prune(now time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{"challenges", "sessions", "bootstrap", "limits"} {
			b := tx.Bucket([]byte(name))
			c := b.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				var record struct {
					ExpiresAt time.Time `json:"expires_at"`
					Start     time.Time
				}
				if err := json.Unmarshal(v, &record); err != nil {
					return err
				}
				expired := !record.ExpiresAt.IsZero() && !now.Before(record.ExpiresAt)
				if name == "limits" {
					expired = now.Sub(record.Start) >= time.Minute
				}
				if expired {
					if err := c.Delete(); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}
