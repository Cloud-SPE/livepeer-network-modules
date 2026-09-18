package credentialstore

import (
	"encoding/json"
	"fmt"
	bolt "go.etcd.io/bbolt"
	"strings"
	"time"
)

type DeviceRevocation struct {
	PoolID     string    `json:"pool_id"`
	HostID     string    `json:"host_id"`
	DeviceID   string    `json:"device_id"`
	Generation uint64    `json:"generation"`
	Reason     string    `json:"reason"`
	At         time.Time `json:"at"`
}

func deviceFenceKey(pool, host, device string) []byte {
	raw, _ := json.Marshal([]string{pool, host, strings.ToLower(device)})
	return raw
}

// RevokeDevice atomically removes the grant and persists a tombstone honored by
// every subsequent credential write, including delayed full controller syncs.
func (s *Store) RevokeDevice(pool, host, device string, generation uint64, reason string) error {
	if pool == "" || host == "" || device == "" || generation == 0 || reason == "" {
		return fmt.Errorf("qualified device revocation required")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("device_revocations"))
		if err != nil {
			return err
		}
		key := deviceFenceKey(pool, host, device)
		if raw := b.Get(key); raw != nil {
			var prior DeviceRevocation
			if err := json.Unmarshal(raw, &prior); err != nil {
				return err
			}
			if prior.Generation >= generation {
				return nil
			}
		}
		id := tx.Bucket([]byte(byHostBucket)).Get([]byte(host))
		if id == nil {
			return ErrNotFound
		}
		rec, err := s.get(tx, string(id))
		if err != nil {
			return err
		}
		device = strings.ToLower(device)
		if rec.PoolID != pool || (rec.DeviceOwnership[device] != generation && rec.DeviceOwnership[device] != 0) {
			return fmt.Errorf("device generation mismatch")
		}
		raw, err := json.Marshal(DeviceRevocation{PoolID: pool, HostID: host, DeviceID: device, Generation: generation, Reason: reason, At: s.opts.Now()})
		if err != nil {
			return err
		}
		if err := b.Put(key, raw); err != nil {
			return err
		}
		return s.put(tx, rec)
	})
}
func filterRevokedDevices(tx *bolt.Tx, rec *Record) error {
	b := tx.Bucket([]byte("device_revocations"))
	if b == nil {
		return nil
	}
	grants := map[string]uint64{}
	for device, generation := range rec.DeviceOwnership {
		device = strings.ToLower(device)
		if raw := b.Get(deviceFenceKey(rec.PoolID, rec.HostID, device)); raw != nil {
			var revocation DeviceRevocation
			if err := json.Unmarshal(raw, &revocation); err != nil {
				return err
			}
			if generation <= revocation.Generation {
				continue
			}
		}
		grants[device] = generation
	}
	rec.DeviceOwnership = grants
	return nil
}
func (s *Store) DeviceRevoked(pool, host, device string, generation uint64) (bool, error) {
	var revoked bool
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("device_revocations"))
		if b == nil {
			return nil
		}
		raw := b.Get(deviceFenceKey(pool, host, device))
		if raw == nil {
			return nil
		}
		var proof DeviceRevocation
		if err := json.Unmarshal(raw, &proof); err != nil {
			return err
		}
		revoked = proof.Generation >= generation
		return nil
	})
	return revoked, err
}
