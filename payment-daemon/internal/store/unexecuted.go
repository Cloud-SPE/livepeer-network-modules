package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	bolt "go.etcd.io/bbolt"
	"math"
	"time"
)

const unexecutedFencesBucket = "unexecuted_authorization_fences"

// CloseUnexecutedAuthorization consumes the trusted broker's durable no-binding
// evidence. The tombstone and zero-use reservation release are one transaction,
// preventing an admission RPC already in flight from creating an orphan later.
func (s *Store) CloseUnexecutedAuthorization(payer, payee []byte, id, reason string) error {
	if len(payer) != 20 || len(payee) != 20 || id == "" || reason == "" {
		return fmt.Errorf("unexecuted authorization identity and reason required")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		key := wholesaleAuthorizationKey(payer, id)
		fences, err := tx.CreateBucketIfNotExists([]byte(unexecutedFencesBucket))
		if err != nil {
			return err
		}
		if fences.Get(key) != nil {
			return nil
		}
		auths := tx.Bucket([]byte(spendAuthorizationsBucket))
		if raw := auths.Get(key); raw != nil {
			var auth WholesaleAuthorization
			if err := json.Unmarshal(raw, &auth); err != nil {
				return err
			}
			if !bytes.Equal(auth.Payee, payee) || auth.ActualUnits != 0 || parseDecimalBig(auth.BilledWei).Sign() != 0 {
				return fmt.Errorf("authorization has usage or inconsistent identity; cannot classify as unexecuted")
			}
			if auth.State == AuthorizationAdmitted {
				account, err := loadWholesaleAccount(tx, payer, payee)
				if err != nil {
					return err
				}
				released := parseDecimalBig(auth.ReservedWei)
				if released.Sign() < 0 || parseDecimalBig(account.ReservedWei).Cmp(released) < 0 || auth.SettlementSeq == math.MaxUint64 {
					return ErrAuthorizationState
				}
				account.ReservedWei = parseDecimalBig(account.ReservedWei).Sub(parseDecimalBig(account.ReservedWei), released).String()
				account.Version++
				account.UpdatedAt = time.Now().UTC()
				auth.State = AuthorizationSettled
				auth.BilledWei = "0"
				auth.ReservedWei = "0"
				auth.ReleasedWei = released.String()
				auth.SettlementSeq++
				auth.UpdatedAt = account.UpdatedAt
				raw, err := json.Marshal(auth)
				if err != nil {
					return err
				}
				if err = auths.Put(key, raw); err != nil {
					return err
				}
				if err = putWholesaleAccount(tx, account); err != nil {
					return err
				}
			} else if auth.State != AuthorizationSettled && auth.State != AuthorizationExpiredUnused && auth.State != AuthorizationSuperseded {
				return ErrAuthorizationState
			}
		}
		raw, err := json.Marshal(struct {
			Reason string    `json:"reason"`
			At     time.Time `json:"at"`
		}{reason, time.Now().UTC()})
		if err != nil {
			return err
		}
		return fences.Put(key, raw)
	})
}
