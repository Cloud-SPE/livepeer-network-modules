package repo

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	bolt "go.etcd.io/bbolt"
)

// OwnershipRepo is a separate shared authority database, never a regional book.
// All state changes and their audit events commit in one transaction.
type OwnershipRepo struct{ db *bolt.DB }
type OwnershipEvent struct {
	Action, Actor string
	At            time.Time
	Before, After ownership.Record
	Evidence      ownership.Request
}

func OpenOwnership(dir string) (*OwnershipRepo, error) {
	if dir == "" {
		return nil, errors.New("ownership data directory required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(dir, "ownership.db"), 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{"devices", "events", "release_receipts"} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		// Index historical releases as well as new ones. The source can replay
		// its own result after the target claims, without reading target identity.
		return tx.Bucket([]byte("events")).ForEach(func(_, raw []byte) error {
			var event OwnershipEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				return err
			}
			if event.Action != "release" {
				return nil
			}
			return tx.Bucket([]byte("release_receipts")).Put(releaseReceiptKey(event.Evidence), raw)
		})
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &OwnershipRepo{db: db}, nil
}
func (r *OwnershipRepo) Close() error      { return r.db.Close() }
func canonicalDevice(device string) string { return strings.ToLower(strings.TrimSpace(device)) }
func (r *OwnershipRepo) Get(device string) (ownership.Record, error) {
	out := ownership.Record{DeviceID: canonicalDevice(device), State: "unclaimed"}
	err := r.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte("devices")).Get([]byte(out.DeviceID))
		if raw == nil {
			return nil
		}
		return json.Unmarshal(raw, &out)
	})
	return out, err
}
func (r *OwnershipRepo) Change(action, actor string, req ownership.Request) (ownership.Record, error) {
	var out ownership.Record
	req.DeviceID = canonicalDevice(req.DeviceID)
	if req.DeviceID == "" || len(req.DeviceID) > 256 || strings.ContainsAny(req.DeviceID, "/\x00") || req.PoolID == "" || req.EnrollmentID == "" || actor == "" {
		return out, errors.New("device, pool, enrollment and actor required")
	}
	err := r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("devices"))
		raw := b.Get([]byte(req.DeviceID))
		before := ownership.Record{DeviceID: req.DeviceID, State: "unclaimed"}
		if raw != nil {
			if err := json.Unmarshal(raw, &before); err != nil {
				return err
			}
		}
		out = before
		if action == "release" {
			if raw := tx.Bucket([]byte("release_receipts")).Get(releaseReceiptKey(req)); raw != nil {
				var event OwnershipEvent
				if err := json.Unmarshal(raw, &event); err != nil {
					return err
				}
				if event.Evidence != req {
					return errors.New("conflicting release retry")
				}
				out = event.After
				return nil
			}
		}
		switch action {
		case "claim":
			if before.State == "active" && before.PoolID == req.PoolID && before.EnrollmentID == req.EnrollmentID && strings.EqualFold(before.MemberWallet, req.MemberWallet) {
				if req.ExpectedGeneration != 0 && req.ExpectedGeneration != before.Generation {
					return errors.New("stale ownership generation")
				}
				return nil
			}
			if before.State != "unclaimed" && (before.State != "released" || before.DestinationPoolID != req.PoolID) {
				return errors.New("device already owned or fenced for another destination")
			}
			if before.Generation != req.ExpectedGeneration || before.Generation == math.MaxUint64 {
				return errors.New("stale or exhausted ownership generation")
			}
			if req.MemberWallet == "" {
				return errors.New("member wallet required")
			}
			if before.State == "released" && !strings.EqualFold(before.MemberWallet, req.MemberWallet) {
				return errors.New("regional transfer preserves member wallet")
			}
			out = ownership.Record{DeviceID: req.DeviceID, PoolID: req.PoolID, EnrollmentID: req.EnrollmentID, MemberWallet: strings.ToLower(req.MemberWallet), Generation: before.Generation + 1, State: "active"}
		case "drain", "release", "fenced-recovery":
			if before.PoolID != req.PoolID || before.EnrollmentID != req.EnrollmentID || before.Generation != req.ExpectedGeneration {
				return errors.New("ownership mismatch or stale generation")
			}
			if req.DestinationPoolID == "" || req.DestinationPoolID == req.PoolID {
				return errors.New("distinct destination pool required")
			}
			if before.State == "released" && before.DestinationPoolID == req.DestinationPoolID {
				return nil
			}
			if action == "drain" {
				if before.State != "active" && before.State != "draining" {
					return errors.New("device is not active")
				}
				if before.State == "draining" && before.DestinationPoolID != req.DestinationPoolID {
					return errors.New("transfer destination is immutable")
				}
				out.State = "draining"
				out.DestinationPoolID = req.DestinationPoolID
			} else {
				if action == "release" && (before.State != "draining" || before.DestinationPoolID != req.DestinationPoolID || req.DrainEvidence == "" || req.RevocationEvidence == "" || req.StopEvidence == "") {
					return errors.New("release requires drain, revocation and stopped-execution evidence")
				}
				if action == "fenced-recovery" && (req.Reason == "" || req.StopEvidence == "" || req.RevocationEvidence == "") {
					return errors.New("recovery requires explicit fencing and revocation evidence")
				}
				out.State = "released"
				out.DestinationPoolID = req.DestinationPoolID
			}
		default:
			return errors.New("unknown ownership action")
		}
		if out == before {
			return nil
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(req.DeviceID), encoded); err != nil {
			return err
		}
		events := tx.Bucket([]byte("events"))
		seq, err := events.NextSequence()
		if err != nil {
			return err
		}
		key := make([]byte, 8)
		binary.BigEndian.PutUint64(key, seq)
		event, err := json.Marshal(OwnershipEvent{Action: action, Actor: actor, At: time.Now().UTC(), Before: before, After: out, Evidence: req})
		if err != nil {
			return err
		}
		if action == "release" {
			if err := tx.Bucket([]byte("release_receipts")).Put(releaseReceiptKey(req), event); err != nil {
				return err
			}
		}
		return events.Put(key, event)
	})
	if err != nil {
		return ownership.Record{}, fmt.Errorf("ownership: %w", err)
	}
	return out, nil
}

func releaseReceiptKey(req ownership.Request) []byte {
	raw, _ := json.Marshal([]any{req.DeviceID, req.PoolID, req.EnrollmentID, req.ExpectedGeneration})
	sum := sha256.Sum256(raw)
	return sum[:]
}
