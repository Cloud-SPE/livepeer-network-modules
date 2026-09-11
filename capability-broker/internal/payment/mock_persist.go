package payment

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

// Optional persistence for the Mock client.
//
// The real payee daemon keeps its ledger in BoltDB and therefore
// survives a broker restart. An in-memory-only mock models something
// that does not exist — a daemon with total amnesia — and that
// difference is not cosmetic: it decides which branch of
// paid-session/v1 §9.2 recovery a restarted session takes. With an
// amnesiac payment layer every session takes the terminal branch, so
// the rebind branch cannot be exercised at all.
//
// Persistence is opt-in via payment_daemon.mock_state_path. Leaving it
// unset keeps the amnesiac behavior, which is itself useful: it is how
// the terminal branch gets exercised deterministically.

type persistedSession struct {
	WorkID              string    `json:"work_id"`
	Sender              []byte    `json:"sender,omitempty"`
	Capability          string    `json:"capability"`
	Offering            string    `json:"offering"`
	PricePerWorkUnitWei string    `json:"price_per_work_unit_wei"`
	PerUnits            uint64    `json:"per_units,omitempty"`
	WorkUnit            string    `json:"work_unit"`
	DebitedUnits        uint64    `json:"debited_units,omitempty"`
	Balance             string    `json:"balance"`
	OpenedAt            time.Time `json:"opened_at"`
	ClosedAt            time.Time `json:"closed_at,omitzero"`
	Closed              bool      `json:"closed"`
	Debits              []int64   `json:"debits,omitempty"`
}

type persistedState struct {
	Sessions       map[string]persistedSession       `json:"sessions"`
	Debits         map[string]int64                  `json:"debits"`
	Accounts       map[string]persistedAccount       `json:"wholesale_accounts,omitempty"`
	Authorizations map[string]persistedAuthorization `json:"spend_authorizations,omitempty"`
}

type persistedAccount struct {
	Payer, Payee                           []byte
	Credited, Reserved, Debited, Available string
	Version, ChainID                       uint64
	ObservedAt, Denomination               string
}

type persistedAuthorization struct {
	Payload                    []byte
	Reserved, Billed, Released string
	State                      int32
}

func decimal(value string) *big.Int {
	n, ok := new(big.Int).SetString(value, 10)
	if !ok || n == nil {
		return new(big.Int)
	}
	return n
}

// EnablePersistence points the mock at a state file, loading any
// existing state immediately. Subsequent mutations are flushed to the
// file, so the ledger survives the process.
func (m *Mock) EnablePersistence(path string) error {
	if path == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statePath = path
	return m.loadLocked()
}

func (m *Mock) loadLocked() error {
	raw, err := os.ReadFile(m.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // first run
		}
		return fmt.Errorf("payment mock: read state: %w", err)
	}
	var st persistedState
	if err := json.Unmarshal(raw, &st); err != nil {
		return fmt.Errorf("payment mock: decode state: %w", err)
	}
	for k, ps := range st.Sessions {
		price, _ := new(big.Int).SetString(ps.PricePerWorkUnitWei, 10)
		if price == nil {
			price = new(big.Int)
		}
		bal, _ := new(big.Int).SetString(ps.Balance, 10)
		if bal == nil {
			bal = new(big.Int)
		}
		m.sessions[k] = &mockSession{
			workID:              ps.WorkID,
			sender:              ps.Sender,
			capability:          ps.Capability,
			offering:            ps.Offering,
			pricePerWorkUnitWei: price,
			perUnits:            ps.PerUnits,
			workUnit:            ps.WorkUnit,
			debitedUnits:        ps.DebitedUnits,
			balance:             bal,
			openedAt:            ps.OpenedAt,
			closedAt:            ps.ClosedAt,
			closed:              ps.Closed,
			debits:              ps.Debits,
		}
	}
	for k, v := range st.Debits {
		m.debits[k] = v
	}
	for k, pa := range st.Accounts {
		m.wholesaleAccounts[k] = &WholesaleAccount{Payer: pa.Payer, Payee: pa.Payee, Credited: decimal(pa.Credited), Reserved: decimal(pa.Reserved), Debited: decimal(pa.Debited), Available: decimal(pa.Available), Version: pa.Version, ObservedAt: pa.ObservedAt, ChainID: pa.ChainID, Denomination: pa.Denomination}
	}
	for k, pa := range st.Authorizations {
		var payload pb.SpendAuthorizationPayload
		if proto.Unmarshal(pa.Payload, &payload) != nil {
			continue
		}
		m.accountAuthorizations[k] = &mockAuthorization{payload: &payload, reserved: decimal(pa.Reserved), billed: decimal(pa.Billed), released: decimal(pa.Released), state: pa.State}
	}
	return nil
}

// flushLocked writes current state. Callers hold m.mu. Errors are
// returned to the caller's error path where one exists; the mock is a
// test surface, so a failed flush must not take the broker down.
func (m *Mock) flushLocked() {
	if m.statePath == "" {
		return
	}
	st := persistedState{
		Sessions:       make(map[string]persistedSession, len(m.sessions)),
		Debits:         make(map[string]int64, len(m.debits)),
		Accounts:       make(map[string]persistedAccount, len(m.wholesaleAccounts)),
		Authorizations: make(map[string]persistedAuthorization, len(m.accountAuthorizations)),
	}
	for k, s := range m.sessions {
		price, bal := "0", "0"
		if s.pricePerWorkUnitWei != nil {
			price = s.pricePerWorkUnitWei.String()
		}
		if s.balance != nil {
			bal = s.balance.String()
		}
		st.Sessions[k] = persistedSession{
			WorkID: s.workID, Sender: s.sender, Capability: s.capability,
			Offering: s.offering, PricePerWorkUnitWei: price, PerUnits: s.perUnits,
			WorkUnit: s.workUnit, DebitedUnits: s.debitedUnits,
			Balance: bal, OpenedAt: s.openedAt, ClosedAt: s.closedAt,
			Closed: s.closed, Debits: s.debits,
		}
	}
	for k, v := range m.debits {
		st.Debits[k] = v
	}
	for k, a := range m.wholesaleAccounts {
		st.Accounts[k] = persistedAccount{Payer: a.Payer, Payee: a.Payee, Credited: a.Credited.String(), Reserved: a.Reserved.String(), Debited: a.Debited.String(), Available: a.Available.String(), Version: a.Version, ObservedAt: a.ObservedAt, ChainID: a.ChainID, Denomination: a.Denomination}
	}
	for k, a := range m.accountAuthorizations {
		payload, err := proto.Marshal(a.payload)
		if err != nil {
			continue
		}
		st.Authorizations[k] = persistedAuthorization{Payload: payload, Reserved: a.reserved.String(), Billed: a.billed.String(), Released: a.released.String(), State: a.state}
	}
	raw, err := json.Marshal(&st)
	if err != nil {
		return
	}
	tmp := m.statePath + ".tmp"
	if err := os.MkdirAll(filepath.Dir(m.statePath), 0o700); err != nil {
		return
	}
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, m.statePath)
}
