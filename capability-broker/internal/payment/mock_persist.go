package payment

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
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
	WorkUnit            string    `json:"work_unit"`
	Balance             string    `json:"balance"`
	OpenedAt            time.Time `json:"opened_at"`
	ClosedAt            time.Time `json:"closed_at,omitzero"`
	Closed              bool      `json:"closed"`
	Debits              []int64   `json:"debits,omitempty"`
}

type persistedState struct {
	Sessions map[string]persistedSession `json:"sessions"`
	Debits   map[string]int64            `json:"debits"`
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
			workUnit:            ps.WorkUnit,
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
		Sessions: make(map[string]persistedSession, len(m.sessions)),
		Debits:   make(map[string]int64, len(m.debits)),
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
			Offering: s.offering, PricePerWorkUnitWei: price, WorkUnit: s.workUnit,
			Balance: bal, OpenedAt: s.openedAt, ClosedAt: s.closedAt,
			Closed: s.closed, Debits: s.debits,
		}
	}
	for k, v := range m.debits {
		st.Debits[k] = v
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
