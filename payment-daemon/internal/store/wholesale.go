package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	AuthorizationAdmitted      = "admitted"
	AuthorizationSettled       = "settled"
	AuthorizationExpiredUnused = "expired_unused"
	AuthorizationSuperseded    = "superseded"
)

var (
	ErrAuthorizationFingerprint = errors.New("store: authorization id reused with different content")
	ErrAuthorizationNotFound    = errors.New("store: spend authorization not found")
	ErrAuthorizationState       = errors.New("store: spend authorization state does not permit operation")
	ErrAuthorizationSettlement  = errors.New("store: settlement replay differs from recorded content")
	ErrInsufficientWholesale    = errors.New("store: insufficient wholesale account balance")
)

// WholesaleAccount is the durable economic ledger for one payer/payee pair.
// Chain and denomination are process configuration in v1, so the two addresses
// fully identify the local record.
type WholesaleAccount struct {
	Payer       []byte    `json:"payer"`
	Payee       []byte    `json:"payee"`
	CreditedWei string    `json:"credited_wei"`
	ReservedWei string    `json:"reserved_wei"`
	DebitedWei  string    `json:"debited_wei"`
	Version     uint64    `json:"version"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// WholesaleTotals is the low-cardinality payee-wide exposure view used for
// metrics. It is updated in the same transaction as every account write.
type WholesaleTotals struct {
	CreditedWei string `json:"credited_wei"`
	ReservedWei string `json:"reserved_wei"`
	DebitedWei  string `json:"debited_wei"`
}

func (t WholesaleTotals) Available() *big.Int {
	return new(big.Int).Sub(parseDecimalBig(t.CreditedWei), new(big.Int).Add(parseDecimalBig(t.ReservedWei), parseDecimalBig(t.DebitedWei)))
}

func (a WholesaleAccount) Available() *big.Int {
	return new(big.Int).Sub(parseDecimalBig(a.CreditedWei), new(big.Int).Add(parseDecimalBig(a.ReservedWei), parseDecimalBig(a.DebitedWei)))
}

type WholesaleAuthorizationSeed struct {
	ID            string
	Fingerprint   []byte
	Payer         []byte
	Payee         []byte
	RequestID     string
	SessionID     string
	Protocol      string
	Capability    string
	Offering      string
	PriceWei      string
	PerUnits      uint64
	WorkUnit      string
	MaxDebitWei   string
	MaxTotalUnits uint64
	ExpiresAt     time.Time
	Revision      uint64
	PredecessorID string
}

type WholesaleAuthorization struct {
	ID            string    `json:"id"`
	Fingerprint   []byte    `json:"fingerprint"`
	Payer         []byte    `json:"payer"`
	Payee         []byte    `json:"payee"`
	RequestID     string    `json:"request_id"`
	SessionID     string    `json:"session_id,omitempty"`
	Protocol      string    `json:"protocol"`
	Capability    string    `json:"capability"`
	Offering      string    `json:"offering"`
	PriceWei      string    `json:"price_wei"`
	PerUnits      uint64    `json:"per_units"`
	WorkUnit      string    `json:"work_unit"`
	MaxDebitWei   string    `json:"max_debit_wei"`
	MaxTotalUnits uint64    `json:"max_total_units"`
	State         string    `json:"state"`
	ActualUnits   uint64    `json:"actual_units,omitempty"`
	BilledWei     string    `json:"billed_wei,omitempty"`
	ReleasedWei   string    `json:"released_wei,omitempty"`
	ReservedWei   string    `json:"reserved_wei,omitempty"`
	SettlementSeq uint64    `json:"settlement_seq,omitempty"`
	ExpiresAt     time.Time `json:"expires_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Revision      uint64    `json:"revision,omitempty"`
	PredecessorID string    `json:"predecessor_id,omitempty"`
}

type WholesaleAdmissionResult struct {
	Account       *WholesaleAccount
	Authorization *WholesaleAuthorization
	Transferred   *big.Int
	Replayed      bool
}

// FundWholesale atomically moves all currently credited value from a ticket
// validation generation into the stable account. Repeating it after a lost
// response transfers zero and returns the same account position.
func (s *Store) FundWholesale(payer, payee []byte, fundingWorkID string, now time.Time) (*WholesaleAccount, *big.Int, error) {
	if len(payer) != 20 || len(payee) != 20 || fundingWorkID == "" {
		return nil, nil, errors.New("store: payer, payee, and funding work id are required")
	}
	transferred := new(big.Int)
	var account *WholesaleAccount
	err := s.db.Update(func(tx *bolt.Tx) error {
		var err error
		account, err = loadWholesaleAccount(tx, payer, payee)
		if err != nil {
			return err
		}
		sessions := tx.Bucket([]byte(sessionsBucket))
		key := compositeKey(payer, fundingWorkID)
		raw := sessions.Get(key)
		if raw == nil {
			return ErrNotFound
		}
		var session Session
		if err := json.Unmarshal(raw, &session); err != nil {
			return err
		}
		transferred.Set(parseDecimalBig(session.BalanceWei))
		if transferred.Sign() == 0 {
			return nil
		}
		account.CreditedWei = new(big.Int).Add(parseDecimalBig(account.CreditedWei), transferred).String()
		account.Version++
		account.UpdatedAt = now.UTC()
		session.BalanceWei = "0"
		updated, err := json.Marshal(&session)
		if err != nil {
			return err
		}
		if err := sessions.Put(key, updated); err != nil {
			return err
		}
		return putWholesaleAccount(tx, account)
	})
	return account, transferred, err
}

// AdmitWholesale atomically migrates any positive balance on fundingWorkID
// into the stable account and reserves the authorization maximum. Moving and
// reserving in one Bolt transaction closes the crash window between legacy
// ticket validation and account admission: a retry either sees the session
// balance still present or the recorded authorization, never an untracked
// transfer.
func (s *Store) AdmitWholesale(seed WholesaleAuthorizationSeed, fundingWorkID string, reservation *big.Int, now time.Time) (*WholesaleAdmissionResult, error) {
	if seed.ID == "" || len(seed.Payer) != 20 || len(seed.Payee) != 20 {
		return nil, errors.New("store: authorization id and 20-byte principals are required")
	}
	result := &WholesaleAdmissionResult{Transferred: new(big.Int)}
	insufficient := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		auths := tx.Bucket([]byte(spendAuthorizationsBucket))
		authKey := wholesaleAuthorizationKey(seed.Payer, seed.ID)
		if raw := auths.Get(authKey); raw != nil {
			var existing WholesaleAuthorization
			if err := json.Unmarshal(raw, &existing); err != nil {
				return err
			}
			if !bytes.Equal(existing.Fingerprint, seed.Fingerprint) {
				return ErrAuthorizationFingerprint
			}
			account, err := loadWholesaleAccount(tx, seed.Payer, seed.Payee)
			if err != nil {
				return err
			}
			result.Account, result.Authorization, result.Replayed = account, &existing, true
			return nil
		}

		account, err := loadWholesaleAccount(tx, seed.Payer, seed.Payee)
		if err != nil {
			return err
		}
		if !seed.ExpiresAt.After(now) {
			auth := &WholesaleAuthorization{
				ID: seed.ID, Fingerprint: bytes.Clone(seed.Fingerprint), Payer: bytes.Clone(seed.Payer), Payee: bytes.Clone(seed.Payee),
				RequestID: seed.RequestID, SessionID: seed.SessionID, Protocol: seed.Protocol,
				Capability: seed.Capability, Offering: seed.Offering, PriceWei: seed.PriceWei,
				PerUnits: seed.PerUnits, WorkUnit: seed.WorkUnit, MaxDebitWei: seed.MaxDebitWei,
				MaxTotalUnits: seed.MaxTotalUnits, State: AuthorizationExpiredUnused,
				Revision: seed.Revision, PredecessorID: seed.PredecessorID,
				ExpiresAt: seed.ExpiresAt.UTC(), UpdatedAt: now.UTC(),
			}
			encoded, err := json.Marshal(auth)
			if err != nil {
				return err
			}
			if err := auths.Put(authKey, encoded); err != nil {
				return err
			}
			result.Account, result.Authorization = account, auth
			return nil
		}
		if fundingWorkID != "" {
			sessionKey := compositeKey(seed.Payer, fundingWorkID)
			sessions := tx.Bucket([]byte(sessionsBucket))
			raw := sessions.Get(sessionKey)
			if raw == nil {
				return ErrNotFound
			}
			var session Session
			if err := json.Unmarshal(raw, &session); err != nil {
				return err
			}
			balance := parseDecimalBig(session.BalanceWei)
			if balance.Sign() > 0 {
				result.Transferred.Set(balance)
				account.CreditedWei = new(big.Int).Add(parseDecimalBig(account.CreditedWei), balance).String()
				session.BalanceWei = "0"
				updated, err := json.Marshal(&session)
				if err != nil {
					return err
				}
				if err := sessions.Put(sessionKey, updated); err != nil {
					return err
				}
			}
		}

		maxDebit := parseDecimalBig(seed.MaxDebitWei)
		inheritedUnits := uint64(0)
		inheritedSeq := uint64(0)
		inheritedBilled := new(big.Int)
		var predecessor *WholesaleAuthorization
		oldReserve := new(big.Int)
		if seed.PredecessorID != "" {
			predKey := wholesaleAuthorizationKey(seed.Payer, seed.PredecessorID)
			predRaw := auths.Get(predKey)
			if predRaw == nil {
				return ErrAuthorizationNotFound
			}
			var pred WholesaleAuthorization
			if err := json.Unmarshal(predRaw, &pred); err != nil {
				return err
			}
			if pred.State != AuthorizationAdmitted || pred.SessionID == "" || pred.SessionID != seed.SessionID || pred.Protocol != seed.Protocol || pred.Capability != seed.Capability || pred.Offering != seed.Offering || pred.PriceWei != seed.PriceWei || pred.PerUnits != seed.PerUnits || pred.WorkUnit != seed.WorkUnit || seed.Revision <= pred.Revision {
				return ErrAuthorizationState
			}
			inheritedUnits, inheritedSeq, inheritedBilled = pred.ActualUnits, pred.SettlementSeq, parseDecimalBig(pred.BilledWei)
			if seed.MaxTotalUnits < inheritedUnits || maxDebit.Cmp(inheritedBilled) < 0 {
				return ErrAuthorizationState
			}
			oldReserve.Set(parseDecimalBig(pred.ReservedWei))
			predecessor = &pred
		}
		if reservation == nil || reservation.Sign() == 0 {
			reservation = new(big.Int).Sub(new(big.Int).Set(maxDebit), inheritedBilled)
		}
		if reservation.Sign() < 0 || reservation.Cmp(new(big.Int).Sub(new(big.Int).Set(maxDebit), inheritedBilled)) > 0 {
			return ErrAuthorizationState
		}
		// A successor atomically replaces its predecessor's reservation.
		// Count that reservation as reusable for the admission decision, but
		// do not release or supersede it until the successor can actually be
		// admitted. Otherwise an underfunded revision would leave a live
		// session with neither its old authority nor its new one.
		availableForAdmission := new(big.Int).Add(account.Available(), oldReserve)
		if availableForAdmission.Cmp(reservation) < 0 {
			if result.Transferred.Sign() > 0 {
				account.Version++
				account.UpdatedAt = now.UTC()
				if err := putWholesaleAccount(tx, account); err != nil {
					return err
				}
			}
			result.Account = account
			insufficient = true
			return nil
		}
		if predecessor != nil {
			account.ReservedWei = new(big.Int).Sub(parseDecimalBig(account.ReservedWei), oldReserve).String()
			predecessor.State, predecessor.ReservedWei, predecessor.ReleasedWei, predecessor.UpdatedAt = AuthorizationSuperseded, "0", new(big.Int).Add(parseDecimalBig(predecessor.ReleasedWei), oldReserve).String(), now.UTC()
			encodedPred, err := json.Marshal(predecessor)
			if err != nil {
				return err
			}
			if err := auths.Put(wholesaleAuthorizationKey(seed.Payer, seed.PredecessorID), encodedPred); err != nil {
				return err
			}
		}
		account.ReservedWei = new(big.Int).Add(parseDecimalBig(account.ReservedWei), reservation).String()
		account.Version++
		account.UpdatedAt = now.UTC()
		auth := &WholesaleAuthorization{
			ID: seed.ID, Fingerprint: bytes.Clone(seed.Fingerprint), Payer: bytes.Clone(seed.Payer), Payee: bytes.Clone(seed.Payee),
			RequestID: seed.RequestID, SessionID: seed.SessionID, Protocol: seed.Protocol,
			Capability: seed.Capability, Offering: seed.Offering, PriceWei: seed.PriceWei,
			PerUnits: seed.PerUnits, WorkUnit: seed.WorkUnit, MaxDebitWei: seed.MaxDebitWei,
			MaxTotalUnits: seed.MaxTotalUnits, State: AuthorizationAdmitted,
			ReservedWei: reservation.String(), ActualUnits: inheritedUnits, BilledWei: inheritedBilled.String(), SettlementSeq: inheritedSeq, Revision: seed.Revision, PredecessorID: seed.PredecessorID,
			ExpiresAt: seed.ExpiresAt.UTC(), UpdatedAt: now.UTC(),
		}
		encoded, err := json.Marshal(auth)
		if err != nil {
			return err
		}
		if err := auths.Put(authKey, encoded); err != nil {
			return err
		}
		if err := putWholesaleAccount(tx, account); err != nil {
			return err
		}
		result.Account, result.Authorization = account, auth
		return nil
	})
	if err == nil && insufficient {
		err = ErrInsufficientWholesale
	}
	return result, err
}

type WholesaleAdvanceResult struct {
	Account       *WholesaleAccount
	Authorization *WholesaleAuthorization
	BilledDelta   *big.Int
	Transferred   *big.Int
	Replayed      bool
}

// AdvanceWholesale atomically moves a session's cumulative billing curve and
// replaces its prior runway reservation. It never reserves beyond the signed
// authorization remainder and never lets two sessions claim the same float.
func (s *Store) AdvanceWholesale(payer, payee []byte, authorizationID string, cumulativeUnits uint64, targetReserved *big.Int, advanceSeq uint64, fundingWorkID string, now time.Time) (*WholesaleAdvanceResult, error) {
	if advanceSeq == 0 || targetReserved == nil || targetReserved.Sign() < 0 {
		return nil, ErrAuthorizationState
	}
	result := &WholesaleAdvanceResult{BilledDelta: new(big.Int), Transferred: new(big.Int)}
	err := s.db.Update(func(tx *bolt.Tx) error {
		auths := tx.Bucket([]byte(spendAuthorizationsBucket))
		key := wholesaleAuthorizationKey(payer, authorizationID)
		raw := auths.Get(key)
		if raw == nil {
			return ErrAuthorizationNotFound
		}
		var auth WholesaleAuthorization
		if err := json.Unmarshal(raw, &auth); err != nil {
			return err
		}
		if !bytes.Equal(auth.Payee, payee) || auth.State != AuthorizationAdmitted || cumulativeUnits > auth.MaxTotalUnits {
			return ErrAuthorizationState
		}
		if !auth.ExpiresAt.After(now) {
			return ErrAuthorizationState
		}
		if advanceSeq < auth.SettlementSeq {
			return ErrAuthorizationSettlement
		}
		if advanceSeq == auth.SettlementSeq {
			if cumulativeUnits != auth.ActualUnits || targetReserved.Cmp(parseDecimalBig(auth.ReservedWei)) != 0 {
				return ErrAuthorizationSettlement
			}
			account, err := loadWholesaleAccount(tx, payer, payee)
			if err != nil {
				return err
			}
			result.Account, result.Authorization, result.Replayed = account, &auth, true
			return nil
		}
		account, err := loadWholesaleAccount(tx, payer, payee)
		if err != nil {
			return err
		}
		if fundingWorkID != "" {
			sessions := tx.Bucket([]byte(sessionsBucket))
			sk := compositeKey(payer, fundingWorkID)
			sr := sessions.Get(sk)
			if sr == nil {
				return ErrNotFound
			}
			var session Session
			if err := json.Unmarshal(sr, &session); err != nil {
				return err
			}
			bal := parseDecimalBig(session.BalanceWei)
			if bal.Sign() > 0 {
				result.Transferred.Set(bal)
				account.CreditedWei = new(big.Int).Add(parseDecimalBig(account.CreditedWei), bal).String()
				session.BalanceWei = "0"
				enc, _ := json.Marshal(&session)
				if err := sessions.Put(sk, enc); err != nil {
					return err
				}
			}
		}
		newBilled := BillFor(parseDecimalBig(auth.PriceWei), auth.PerUnits, cumulativeUnits)
		oldBilled := parseDecimalBig(auth.BilledWei)
		if newBilled.Cmp(oldBilled) < 0 || newBilled.Cmp(parseDecimalBig(auth.MaxDebitWei)) > 0 {
			return ErrAuthorizationState
		}
		delta := new(big.Int).Sub(newBilled, oldBilled)
		remaining := new(big.Int).Sub(parseDecimalBig(auth.MaxDebitWei), newBilled)
		if targetReserved.Cmp(remaining) > 0 {
			return ErrAuthorizationState
		}
		oldReserved := parseDecimalBig(auth.ReservedWei)
		availableAfterRelease := new(big.Int).Add(account.Available(), oldReserved)
		if availableAfterRelease.Cmp(new(big.Int).Add(delta, targetReserved)) < 0 {
			return ErrInsufficientWholesale
		}
		account.ReservedWei = new(big.Int).Add(new(big.Int).Sub(parseDecimalBig(account.ReservedWei), oldReserved), targetReserved).String()
		account.DebitedWei = new(big.Int).Add(parseDecimalBig(account.DebitedWei), delta).String()
		account.Version++
		account.UpdatedAt = now.UTC()
		auth.ActualUnits, auth.BilledWei, auth.ReservedWei, auth.SettlementSeq, auth.UpdatedAt = cumulativeUnits, newBilled.String(), targetReserved.String(), advanceSeq, now.UTC()
		enc, err := json.Marshal(&auth)
		if err != nil {
			return err
		}
		if err := auths.Put(key, enc); err != nil {
			return err
		}
		if err := putWholesaleAccount(tx, account); err != nil {
			return err
		}
		result.Account, result.Authorization, result.BilledDelta = account, &auth, delta
		return nil
	})
	return result, err
}

type WholesaleSettlementResult struct {
	Account       *WholesaleAccount
	Authorization *WholesaleAuthorization
	Billed        *big.Int
	Released      *big.Int
	Replayed      bool
}

func (s *Store) SettleWholesale(payer, payee []byte, authorizationID string, actualUnits, settlementSeq uint64, now time.Time) (*WholesaleSettlementResult, error) {
	result := &WholesaleSettlementResult{Billed: new(big.Int), Released: new(big.Int)}
	err := s.db.Update(func(tx *bolt.Tx) error {
		auths := tx.Bucket([]byte(spendAuthorizationsBucket))
		key := wholesaleAuthorizationKey(payer, authorizationID)
		raw := auths.Get(key)
		if raw == nil {
			return ErrAuthorizationNotFound
		}
		var auth WholesaleAuthorization
		if err := json.Unmarshal(raw, &auth); err != nil {
			return err
		}
		if !bytes.Equal(auth.Payee, payee) {
			return ErrAuthorizationNotFound
		}
		if auth.State == AuthorizationSettled {
			if auth.ActualUnits != actualUnits || auth.SettlementSeq != settlementSeq {
				return ErrAuthorizationSettlement
			}
			account, err := loadWholesaleAccount(tx, payer, payee)
			if err != nil {
				return err
			}
			result.Account, result.Authorization, result.Replayed = account, &auth, true
			result.Billed = parseDecimalBig(auth.BilledWei)
			result.Released = parseDecimalBig(auth.ReleasedWei)
			return nil
		}
		if auth.State != AuthorizationAdmitted || actualUnits > auth.MaxTotalUnits {
			return ErrAuthorizationState
		}
		if settlementSeq <= auth.SettlementSeq {
			return ErrAuthorizationSettlement
		}
		billed := BillFor(parseDecimalBig(auth.PriceWei), auth.PerUnits, actualUnits)
		maxDebit := parseDecimalBig(auth.MaxDebitWei)
		if billed.Cmp(maxDebit) > 0 {
			return ErrAuthorizationState
		}
		priorBilled := parseDecimalBig(auth.BilledWei)
		if billed.Cmp(priorBilled) < 0 {
			return ErrAuthorizationState
		}
		billedDelta := new(big.Int).Sub(billed, priorBilled)
		released := parseDecimalBig(auth.ReservedWei)
		account, err := loadWholesaleAccount(tx, payer, payee)
		if err != nil {
			return err
		}
		if released.Cmp(billedDelta) < 0 {
			return ErrAuthorizationState
		}
		account.ReservedWei = new(big.Int).Sub(parseDecimalBig(account.ReservedWei), released).String()
		account.DebitedWei = new(big.Int).Add(parseDecimalBig(account.DebitedWei), billedDelta).String()
		account.Version++
		account.UpdatedAt = now.UTC()
		auth.State, auth.ActualUnits, auth.SettlementSeq = AuthorizationSettled, actualUnits, settlementSeq
		auth.BilledWei, auth.ReleasedWei, auth.ReservedWei, auth.UpdatedAt = billed.String(), new(big.Int).Sub(released, billedDelta).String(), "0", now.UTC()
		encoded, err := json.Marshal(&auth)
		if err != nil {
			return err
		}
		if err := auths.Put(key, encoded); err != nil {
			return err
		}
		if err := putWholesaleAccount(tx, account); err != nil {
			return err
		}
		result.Account, result.Authorization = account, &auth
		result.Billed, result.Released = billed, new(big.Int).Sub(released, billedDelta)
		return nil
	})
	return result, err
}

func (s *Store) GetWholesaleAccount(payer, payee []byte) (*WholesaleAccount, error) {
	var account *WholesaleAccount
	err := s.db.View(func(tx *bolt.Tx) error {
		var err error
		account, err = loadWholesaleAccount(tx, payer, payee)
		return err
	})
	return account, err
}

func (s *Store) GetWholesaleAuthorization(payer []byte, id string) (*WholesaleAuthorization, error) {
	var auth WholesaleAuthorization
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(spendAuthorizationsBucket)).Get(wholesaleAuthorizationKey(payer, id))
		if raw == nil {
			return ErrAuthorizationNotFound
		}
		return json.Unmarshal(raw, &auth)
	})
	return &auth, err
}

func (s *Store) GetWholesaleTotals() (*WholesaleTotals, error) {
	var totals WholesaleTotals
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(wholesaleTotalsBucket)).Get([]byte("totals"))
		if raw == nil {
			return nil
		}
		return json.Unmarshal(raw, &totals)
	})
	return &totals, err
}

func loadWholesaleAccount(tx *bolt.Tx, payer, payee []byte) (*WholesaleAccount, error) {
	key := wholesaleAccountKey(payer, payee)
	raw := tx.Bucket([]byte(wholesaleAccountsBucket)).Get(key)
	if raw == nil {
		return &WholesaleAccount{Payer: bytes.Clone(payer), Payee: bytes.Clone(payee), CreditedWei: "0", ReservedWei: "0", DebitedWei: "0"}, nil
	}
	var account WholesaleAccount
	if err := json.Unmarshal(raw, &account); err != nil {
		return nil, fmt.Errorf("unmarshal wholesale account: %w", err)
	}
	return &account, nil
}

func putWholesaleAccount(tx *bolt.Tx, account *WholesaleAccount) error {
	accounts := tx.Bucket([]byte(wholesaleAccountsBucket))
	key := wholesaleAccountKey(account.Payer, account.Payee)
	var previous WholesaleAccount
	if raw := accounts.Get(key); raw != nil {
		if err := json.Unmarshal(raw, &previous); err != nil {
			return err
		}
	}
	totalsBucket := tx.Bucket([]byte(wholesaleTotalsBucket))
	var totals WholesaleTotals
	if raw := totalsBucket.Get([]byte("totals")); raw != nil {
		if err := json.Unmarshal(raw, &totals); err != nil {
			return err
		}
	}
	adjust := func(total, before, after string) string {
		return new(big.Int).Add(parseDecimalBig(total), new(big.Int).Sub(parseDecimalBig(after), parseDecimalBig(before))).String()
	}
	totals.CreditedWei = adjust(totals.CreditedWei, previous.CreditedWei, account.CreditedWei)
	totals.ReservedWei = adjust(totals.ReservedWei, previous.ReservedWei, account.ReservedWei)
	totals.DebitedWei = adjust(totals.DebitedWei, previous.DebitedWei, account.DebitedWei)
	totalsRaw, err := json.Marshal(&totals)
	if err != nil {
		return err
	}
	if err := totalsBucket.Put([]byte("totals"), totalsRaw); err != nil {
		return err
	}
	raw, err := json.Marshal(account)
	if err != nil {
		return err
	}
	return accounts.Put(key, raw)
}

func wholesaleAccountKey(payer, payee []byte) []byte {
	h := sha256.New()
	h.Write(payer)
	h.Write(payee)
	return h.Sum(nil)
}

func wholesaleAuthorizationKey(payer []byte, id string) []byte {
	h := sha256.New()
	h.Write(payer)
	h.Write([]byte{0})
	h.Write([]byte(id))
	return h.Sum(nil)
}
