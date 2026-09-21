package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/identity"
	bolt "go.etcd.io/bbolt"
)

// FundingTicket is cryptographically validated before entering the transaction.
// The nonce, winning ticket queue, account credit and receipt commit together.
type FundingTicket struct {
	Nonce  uint32
	Credit *big.Int
	Winner *SignedTicket
	Hash   []byte
}

type FundingReceipt struct {
	FundingID   string            `json:"funding_id"`
	Account     *WholesaleAccount `json:"account"`
	CreditedWei string            `json:"credited_wei"`
}

func (s *Store) GetFundingReceipt(payer, payee []byte, accountID, fundingID string) (*FundingReceipt, error) {
	var result *FundingReceipt
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("wholesale_funding_receipts"))
		if b == nil {
			return ErrNotFound
		}
		raw := b.Get([]byte(fundingID))
		if raw == nil {
			return ErrNotFound
		}
		var r FundingReceipt
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		if r.Account == nil || !bytes.Equal(r.Account.Payer, payer) || !bytes.Equal(r.Account.Payee, payee) || r.Account.WholesaleAccountID != accountID {
			return ErrSenderMismatch
		}
		result = &r
		return nil
	})
	return result, err
}

func (s *Store) ApplyWholesaleFunding(payer, payee []byte, accountID, workID, fundingID string, tickets []FundingTicket, now time.Time) (*FundingReceipt, bool, error) {
	if !identity.ValidWholesaleAccountID(accountID) || len(fundingID) != 64 || len(tickets) == 0 {
		return nil, false, errors.New("funding identity and tickets required")
	}
	var receipt *FundingReceipt
	replayed := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		receipts, err := tx.CreateBucketIfNotExists([]byte("wholesale_funding_receipts"))
		if err != nil {
			return err
		}
		if raw := receipts.Get([]byte(fundingID)); raw != nil {
			var r FundingReceipt
			if err := json.Unmarshal(raw, &r); err != nil {
				return err
			}
			if r.Account == nil || !bytes.Equal(r.Account.Payer, payer) || !bytes.Equal(r.Account.Payee, payee) || r.Account.WholesaleAccountID != accountID {
				return ErrSenderMismatch
			}
			receipt = &r
			replayed = true
			return nil
		}
		raw := tx.Bucket([]byte(sessionsBucket)).Get(compositeKey(payer, workID))
		if raw == nil {
			return ErrNotFound
		}
		var session Session
		if err := json.Unmarshal(raw, &session); err != nil {
			return err
		}
		if session.Closed {
			return ErrClosed
		}
		if session.WholesaleAccountID != accountID || session.TicketStreamID == "" || !bytes.Equal(session.Recipient, payee) {
			return ErrSenderMismatch
		}
		rand, ok := new(big.Int).SetString(session.RecipientRand, 10)
		if !ok {
			return errors.New("invalid generation rand")
		}
		credit := new(big.Int)
		for _, t := range tickets {
			if t.Credit == nil || t.Credit.Sign() <= 0 {
				return errors.New("invalid credit")
			}
			if err := recordNonce(tx, rand, t.Nonce); err != nil {
				return err
			}
			credit.Add(credit, t.Credit)
			if t.Winner != nil {
				encoded, err := json.Marshal(t.Winner)
				if err != nil {
					return err
				}
				if _, err = enqueueRedemption(tx, t.Hash, encoded); err != nil {
					return err
				}
			}
		}
		account, err := loadWholesaleAccount(tx, payer, payee, accountID)
		if err != nil {
			return err
		}
		account.CreditedWei = new(big.Int).Add(parseDecimalBig(account.CreditedWei), credit).String()
		account.Version++
		account.UpdatedAt = now.UTC()
		if err = putWholesaleAccount(tx, account); err != nil {
			return err
		}
		receipt = &FundingReceipt{FundingID: fundingID, Account: account, CreditedWei: credit.String()}
		encoded, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		return receipts.Put([]byte(fundingID), encoded)
	})
	return receipt, replayed, err
}
