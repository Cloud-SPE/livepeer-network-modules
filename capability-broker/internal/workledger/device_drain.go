package workledger

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"strings"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

// Device drains are permanent for an assignment generation. Returning to the
// same pool requires a new authority generation, never deletion of this fence.
type DeviceDrain = ownership.DeviceDrainProof

func deviceDrainKey(host, device string, generation uint64) []byte {
	raw, _ := json.Marshal([]any{host, strings.ToLower(device), generation})
	return raw
}
func (c *Client) BeginDeviceDrain(host, device string, generation uint64, reason string) error {
	if host == "" || device == "" || generation == 0 || strings.TrimSpace(reason) == "" {
		return fmt.Errorf("device drain requires enrollment, device, generation and reason")
	}
	c.admissionGate.Lock()
	defer c.admissionGate.Unlock()
	return c.Store.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("device_drains"))
		if err != nil {
			return err
		}
		key := deviceDrainKey(host, device, generation)
		if b.Get(key) != nil {
			return nil
		}
		raw, err := json.Marshal(DeviceDrain{PoolID: c.Store.PoolID, SourceID: c.Store.SourceID, BrokerID: c.Store.BrokerID, EnrollmentID: host, DeviceID: strings.ToLower(device), Generation: generation, Reason: reason, StartedAt: time.Now().UTC()})
		if err != nil {
			return err
		}
		return b.Put(key, raw)
	})
}
func bindingDraining(tx *bolt.Tx, a Attribution) bool {
	b := tx.Bucket([]byte("device_drains"))
	if b == nil {
		return false
	}
	for device, generation := range a.DeviceOwnership {
		if b.Get(deviceDrainKey(a.Enrollment, device, generation)) != nil {
			return true
		}
	}
	return false
}
func (s *Store) DeviceDraining(host, device string, generation uint64) (bool, error) {
	var draining bool
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("device_drains"))
		draining = b != nil && b.Get(deviceDrainKey(host, device, generation)) != nil
		return nil
	})
	return draining, err
}
func (s *Store) authorizationDeviceDraining(id string) (bool, error) {
	var draining bool
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte("bindings")).Get([]byte(id))
		if raw == nil {
			return nil
		}
		var a Attribution
		if err := json.Unmarshal(raw, &a); err != nil {
			return err
		}
		draining = bindingDraining(tx, a)
		return nil
	})
	return draining, err
}
func (c *Client) DeviceDrainStatus(ctx context.Context, host, device string, generation uint64) (DeviceDrain, error) {
	var out DeviceDrain
	var auths []pb.SpendAuthorization
	// Lock admission through receiver verification. Existing execution may settle;
	// any nonterminal observation keeps the transfer held until a later retry.
	c.admissionGate.Lock()
	defer c.admissionGate.Unlock()
	err := c.Store.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("device_drains"))
		if b == nil {
			return fmt.Errorf("device is not draining")
		}
		raw := b.Get(deviceDrainKey(host, device, generation))
		if raw == nil {
			return fmt.Errorf("device is not draining")
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return err
		}
		out.UnqualifiedWork = uint64(tx.Bucket([]byte("unbound_admissions")).Stats().KeyN)
		matches := map[string]bool{}
		if err := tx.Bucket([]byte("bindings")).ForEach(func(id, raw []byte) error {
			var a Attribution
			if err := json.Unmarshal(raw, &a); err != nil {
				return err
			}
			if a.Enrollment != host {
				return nil
			}
			if len(a.DeviceOwnership) == 0 {
				out.UnqualifiedWork++
				return nil
			}
			if a.DeviceOwnership[strings.ToLower(device)] != generation {
				return nil
			}
			matches[string(id)] = true
			rawAuth := tx.Bucket([]byte("authorizations")).Get(id)
			if rawAuth == nil {
				out.UnqualifiedWork++
				return nil
			}
			var auth pb.SpendAuthorization
			if err := proto.Unmarshal(rawAuth, &auth); err != nil {
				return err
			}
			auths = append(auths, auth)
			return nil
		}); err != nil {
			return err
		}
		return tx.Bucket([]byte("operations")).ForEach(func(_, raw []byte) error {
			var op Operation
			if err := json.Unmarshal(raw, &op); err != nil {
				return err
			}
			if !matches[op.AuthorizationID] {
				return nil
			}
			if !op.Emitted {
				out.PendingOperations++
				return nil
			}
			key := []byte(c.Store.SourceID + "/" + op.ID)
			if tx.Bucket([]byte("receipts")).Get(key) != nil && tx.Bucket([]byte("delivered")).Get(key) == nil {
				out.UndeliveredReceipts++
			}
			return nil
		})
	})
	if err != nil {
		return out, err
	}
	for _, auth := range auths {
		p := auth.GetPayload()
		if p == nil || c.AccountClient == nil {
			return out, fmt.Errorf("receiver authorization proof unavailable")
		}
		state, err := c.AccountClient.GetSpendAuthorization(ctx, p.Payer, p.AuthorizationId, p.GetWholesaleAccountId())
		if err != nil {
			return out, err
		}
		if state == nil {
			return out, fmt.Errorf("receiver authorization proof missing")
		}
		switch state.State {
		case int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_EXPIRED_UNUSED), int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED), int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SUPERSEDED):
		default:
			out.ActiveAuthorizations++
		}
	}
	out.ObservedAt = time.Now().UTC()
	return out, nil
}
