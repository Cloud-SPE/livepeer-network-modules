// Package workledger durably binds accepted billing to regional member work.
package workledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/receipts"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	bolt "go.etcd.io/bbolt"
)

type Attribution struct {
	DeviceOwnership map[string]uint64 `json:"device_ownership,omitempty"`
	TermsVersion    string            `json:"terms_version,omitempty"`
	Member          string            `json:"member"`
	Backend         string            `json:"backend"`
	Enrollment      string            `json:"enrollment"`
	Capability      string            `json:"capability"`
	Offering        string            `json:"offering"`
	RequestID       string            `json:"request_id"`
}

type Operation struct {
	WholesaleAccountID string `json:"wholesale_account_id,omitempty"`
	ID                 string
	AuthorizationID    string
	Kind               string
	Sequence           uint64
	Payer              []byte
	Units              uint64
	TargetReserved     string
	PaymentBytes       []byte
	MinimumRound       int64
	Completed          bool
	TotalBilled        string
	Emitted            bool
}

type Store struct {
	db                         *bolt.DB
	PoolID, SourceID, BrokerID string
}

func Open(path, pool, source, broker string) (*Store, error) {
	if path == "" || pool == "" || source == "" || broker == "" {
		return nil, fmt.Errorf("work ledger identity/path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, PoolID: pool, SourceID: source, BrokerID: broker}
	err = db.Update(func(tx *bolt.Tx) error {
		existing := tx.Bucket([]byte("meta")) != nil
		for _, name := range []string{"meta", "inherited_baselines", "unbound_admissions", "unbound_rounds", "authorizations", "bindings", "operations", "billed", "units", "receipts", "delivered"} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		if tx.Bucket([]byte("meta")).Get([]byte("unbound_indexed")) == nil {
			if err := tx.Bucket([]byte("authorizations")).ForEach(func(id, raw []byte) error {
				if tx.Bucket([]byte("bindings")).Get(id) == nil {
					return tx.Bucket([]byte("unbound_admissions")).Put(id, raw)
				}
				return nil
			}); err != nil {
				return err
			}
			if err := tx.Bucket([]byte("meta")).Put([]byte("unbound_indexed"), []byte{1}); err != nil {
				return err
			}
		}
		identity, _ := json.Marshal([]string{pool, source, broker})
		meta := tx.Bucket([]byte("meta"))
		if prior := meta.Get([]byte("identity")); prior != nil {
			if !bytes.Equal(prior, identity) {
				return fmt.Errorf("work ledger pool/source identity cannot change")
			}
			return nil
		}
		if existing {
			return fmt.Errorf("existing work ledger identity is missing")
		}
		return meta.Put([]byte("identity"), identity)
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }

// SaveAuthorization preserves the accepted signed price and authorization
// alongside billing evidence. It is never reconstructed from today's offer.
func (s *Store) SaveAuthorization(id string, raw []byte) error {
	if id == "" || len(raw) == 0 {
		return fmt.Errorf("authorization evidence required")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("authorizations"))
		if previous := b.Get([]byte(id)); previous != nil && !bytes.Equal(previous, raw) {
			return fmt.Errorf("authorization evidence changed")
		}
		if tx.Bucket([]byte("bindings")).Get([]byte(id)) == nil {
			if err := tx.Bucket([]byte("unbound_rounds")).Put([]byte(id), []byte(strconv.FormatInt(readRound(tx.Bucket([]byte("meta"))), 10))); err != nil {
				return err
			}
			if err := tx.Bucket([]byte("unbound_admissions")).Put([]byte(id), raw); err != nil {
				return err
			}
		}
		return b.Put([]byte(id), raw)
	})
}

func (s *Store) Bind(auth string, a Attribution) error {
	if auth == "" || a.Member == "" || a.Backend == "" || a.Enrollment == "" || a.Capability == "" || a.Offering == "" {
		return fmt.Errorf("member/backend work attribution required")
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("bindings"))
		if old := b.Get([]byte(auth)); old != nil && !bytes.Equal(old, raw) {
			return fmt.Errorf("authorization work binding cannot change")
		}
		if b.Get([]byte(auth)) == nil && bindingDraining(tx, a) {
			return fmt.Errorf("device assignment is draining")
		}
		if err := tx.Bucket([]byte("unbound_rounds")).Delete([]byte(auth)); err != nil {
			return err
		}
		if err := tx.Bucket([]byte("unbound_admissions")).Delete([]byte(auth)); err != nil {
			return err
		}
		return b.Put([]byte(auth), raw)
	})
}
func (s *Store) Binding(auth string) (Attribution, error) {
	var a Attribution
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte("bindings")).Get([]byte(auth))
		if raw == nil {
			return fmt.Errorf("work binding missing")
		}
		return json.Unmarshal(raw, &a)
	})
	return a, err
}

func operationID(auth string, seq uint64) string { return auth + "/" + fmt.Sprintf("%020d", seq) }
func (s *Store) Prepare(op Operation) (Operation, error) {
	op.ID = operationID(op.AuthorizationID, op.Sequence)
	if op.AuthorizationID == "" || op.Sequence == 0 || (op.Kind != "advance" && op.Kind != "settle") {
		return op, fmt.Errorf("invalid billing operation")
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		if op.Units > 0 && tx.Bucket([]byte("bindings")).Get([]byte(op.AuthorizationID)) == nil {
			return fmt.Errorf("billing lacks durable member attribution")
		}
		if raw := tx.Bucket([]byte("authorizations")).Get([]byte(op.AuthorizationID)); raw != nil {
			var auth pb.SpendAuthorization
			if err := proto.Unmarshal(raw, &auth); err != nil {
				return err
			}
			if auth.GetPayload().GetWholesaleAccountId() != op.WholesaleAccountID {
				return fmt.Errorf("billing operation account differs from saved authorization")
			}
			if auth.GetPayload().GetPredecessorAuthorizationId() != "" && tx.Bucket([]byte("inherited_baselines")).Get([]byte(op.AuthorizationID)) == nil {
				return fmt.Errorf("successor inherited billing baseline unavailable")
			}
		}
		bucket := tx.Bucket([]byte("operations"))
		if raw := bucket.Get([]byte(op.ID)); raw != nil {
			var prior Operation
			if err := json.Unmarshal(raw, &prior); err != nil {
				return err
			}
			same := prior
			same.MinimumRound = 0
			same.Completed = false
			same.TotalBilled = ""
			same.Emitted = false
			a, _ := json.Marshal(same)
			b, _ := json.Marshal(op)
			if !bytes.Equal(a, b) {
				return fmt.Errorf("billing sequence reused with different operation")
			}
			op = prior
			return nil
		}
		op.MinimumRound = readRound(tx.Bucket([]byte("meta")))
		raw, err := json.Marshal(op)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(op.ID), raw)
	})
	return op, err
}
func (s *Store) Complete(id string, cumulative *big.Int) error {
	if cumulative == nil || cumulative.Sign() < 0 {
		return fmt.Errorf("invalid finalized billed amount")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("operations"))
		var op Operation
		if err := json.Unmarshal(bucket.Get([]byte(id)), &op); err != nil {
			return err
		}
		if op.Completed && op.TotalBilled != cumulative.String() {
			return fmt.Errorf("billing replay amount changed")
		}
		op.Completed = true
		if op.Kind == "settle" && op.Units == 0 && cumulative.Sign() == 0 {
			if err := tx.Bucket([]byte("unbound_admissions")).Delete([]byte(op.AuthorizationID)); err != nil {
				return err
			}
			if err := tx.Bucket([]byte("unbound_rounds")).Delete([]byte(op.AuthorizationID)); err != nil {
				return err
			}
		}
		op.TotalBilled = cumulative.String()
		raw, err := json.Marshal(op)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(id), raw)
	})
}
func (s *Store) Pending() ([]Operation, error) {
	var out []Operation
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("operations")).ForEach(func(_, raw []byte) error {
			var op Operation
			if err := json.Unmarshal(raw, &op); err != nil {
				return err
			}
			if !op.Completed {
				out = append(out, op)
			}
			return nil
		})
	})
	return out, err
}

// Finalize assigns each durable billed delta to its bookkeeping finalization
// round. Clock outages leave operations durable and unqualified; they never
// invent a round or rewrite a previously closed work-report prefix.
func (s *Store) Finalize(round int64, now time.Time) error {
	if round <= 0 {
		return fmt.Errorf("observed finalization round unavailable")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte("meta"))
		if round < readRound(meta) {
			return fmt.Errorf("observed round regressed")
		}
		blocked := map[string]bool{}
		operations := tx.Bucket([]byte("operations"))
		var updates []Operation
		if err := operations.ForEach(func(_, raw []byte) error {
			var op Operation
			if err := json.Unmarshal(raw, &op); err != nil {
				return err
			}
			if !op.Completed {
				blocked[op.AuthorizationID] = true
				return nil
			}
			if op.Emitted || blocked[op.AuthorizationID] {
				return nil
			}
			total, ok := new(big.Int).SetString(op.TotalBilled, 10)
			if !ok {
				return fmt.Errorf("invalid billed total")
			}
			prior := new(big.Int)
			if raw := tx.Bucket([]byte("billed")).Get([]byte(op.AuthorizationID)); raw != nil {
				if _, ok := prior.SetString(string(raw), 10); !ok {
					return fmt.Errorf("invalid prior billed total")
				}
			}
			priorUnits := uint64(0)
			if raw := tx.Bucket([]byte("units")).Get([]byte(op.AuthorizationID)); raw != nil {
				var err error
				priorUnits, err = strconv.ParseUint(string(raw), 10, 64)
				if err != nil {
					return err
				}
			}
			if op.Units < priorUnits {
				return fmt.Errorf("cumulative billed units regressed")
			}
			deltaUnits := op.Units - priorUnits
			delta := new(big.Int).Sub(total, prior)
			if delta.Sign() < 0 {
				return fmt.Errorf("cumulative billed amount regressed")
			}
			if delta.Sign() > 0 {
				var a Attribution
				if err := json.Unmarshal(tx.Bucket([]byte("bindings")).Get([]byte(op.AuthorizationID)), &a); err != nil {
					return fmt.Errorf("accepted billing has no member binding")
				}
				receipt := receipts.WorkReceipt{TermsVersion: a.TermsVersion, PoolID: s.PoolID, SourceID: s.SourceID, ID: s.SourceID + "/" + op.ID, CreatedAt: now, RoundID: strconv.FormatInt(round, 10), RequestID: a.RequestID, CapabilityID: a.Capability, OfferingID: a.Offering, MemberEthAddress: a.Member, BackendID: a.Backend, HostEnrollmentID: a.Enrollment, ActualUnits: deltaUnits, AcceptedWorkUnits: deltaUnits, GatewayRevenueWei: delta.String(), AttributedRevenueWei: delta.String(), Status: "final"}
				encoded, err := json.Marshal(receipt)
				if err != nil {
					return err
				}
				if err := tx.Bucket([]byte("receipts")).Put([]byte(receipt.ID), encoded); err != nil {
					return err
				}
			}
			if err := tx.Bucket([]byte("billed")).Put([]byte(op.AuthorizationID), []byte(total.String())); err != nil {
				return err
			}
			if err := tx.Bucket([]byte("units")).Put([]byte(op.AuthorizationID), []byte(strconv.FormatUint(op.Units, 10))); err != nil {
				return err
			}
			op.Emitted = true
			updates = append(updates, op)
			return nil
		}); err != nil {
			return err
		}
		for _, op := range updates {
			raw, err := json.Marshal(op)
			if err != nil {
				return err
			}
			if err := operations.Put([]byte(op.ID), raw); err != nil {
				return err
			}
		}
		if err := meta.Put([]byte("round"), []byte(strconv.FormatInt(round, 10))); err != nil {
			return err
		}
		return meta.Put([]byte("observed_at"), []byte(now.UTC().Format(time.RFC3339Nano)))
	})
}
func readRound(meta *bolt.Bucket) int64 {
	n, _ := strconv.ParseInt(string(meta.Get([]byte("round"))), 10, 64)
	return n
}

func (s *Store) Undelivered(limit int) ([]receipts.WorkReceipt, error) {
	out := []receipts.WorkReceipt{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("receipts")).ForEach(func(key, raw []byte) error {
			if tx.Bucket([]byte("delivered")).Get(key) != nil || limit > 0 && len(out) >= limit {
				return nil
			}
			var receipt receipts.WorkReceipt
			if err := json.Unmarshal(raw, &receipt); err != nil {
				return err
			}
			out = append(out, receipt)
			return nil
		})
	})
	return out, err
}
func (s *Store) Delivered(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("delivered")).Put([]byte(id), []byte{1}) })
}

func (s *Store) Report(round int64, now time.Time) (revenue.WorkReport, error) {
	report := revenue.WorkReport{PoolID: s.PoolID, SourceID: s.SourceID, BrokerID: s.BrokerID, Round: round}
	err := s.db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte("meta"))
		report.ClosedThroughRound = readRound(meta) - 1
		report.ObservedAt, _ = time.Parse(time.RFC3339Nano, string(meta.Get([]byte("observed_at"))))
		if report.ClosedThroughRound < round || report.ObservedAt.IsZero() || now.Sub(report.ObservedAt) > 2*time.Minute {
			report.IncompleteReason = "fresh round observation unavailable"
			return nil
		}
		if err := tx.Bucket([]byte("unbound_admissions")).ForEach(func(id, _ []byte) error {
			minimum, _ := strconv.ParseInt(string(tx.Bucket([]byte("unbound_rounds")).Get(id)), 10, 64)
			if minimum <= round {
				report.IncompleteReason = "admission awaits runner binding or fenced recovery"
			}
			return nil
		}); err != nil {
			return err
		}
		if err := tx.Bucket([]byte("operations")).ForEach(func(_, raw []byte) error {
			var op Operation
			if err := json.Unmarshal(raw, &op); err != nil {
				return err
			}
			if !op.Emitted && op.MinimumRound <= round {
				report.IncompleteReason = "billing operations await recovery or finalization"
			}
			return nil
		}); err != nil {
			return err
		}
		if report.IncompleteReason != "" {
			return nil
		}
		entries := []revenue.Contribution{}
		if err := tx.Bucket([]byte("receipts")).ForEach(func(_, raw []byte) error {
			var receipt receipts.WorkReceipt
			if err := json.Unmarshal(raw, &receipt); err != nil {
				return err
			}
			if receipt.RoundID == strconv.FormatInt(round, 10) {
				entries = append(entries, revenue.Contribution{TermsVersion: receipt.TermsVersion, ID: receipt.ID, PoolID: receipt.PoolID, SourceID: receipt.SourceID, RoundID: receipt.RoundID, Member: receipt.MemberEthAddress, Offering: receipt.OfferingID, Backend: receipt.BackendID, AmountWei: receipt.AttributedRevenueWei})
			}
			return nil
		}); err != nil {
			return err
		}
		hash, err := revenue.WorkDigest(entries)
		if err != nil {
			return err
		}
		report.ReceiptDigest = hash
		report.ReceiptCount = uint64(len(entries))
		report.Complete = true
		return nil
	})
	return report, err
}

// KnownBoundAuthorization permits read-only admission replay during drain; an
// unseen or unbound authorization must never gain admission through this path.
func (s *Store) KnownBoundAuthorization(id string, raw []byte) (bool, error) {
	var known bool
	err := s.db.View(func(tx *bolt.Tx) error {
		saved := tx.Bucket([]byte("authorizations")).Get([]byte(id))
		known = saved != nil && bytes.Equal(saved, raw) && tx.Bucket([]byte("bindings")).Get([]byte(id)) != nil
		return nil
	})
	return known, err
}
