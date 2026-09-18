package txintent

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/config"
	cerrors "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/errors"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/store"
	"github.com/vmihailenco/msgpack/v5"
)

// BoltDB bucket names. Stable across versions; renames require a migration.
const (
	bucketIntents       = "chain_commons_tx_intents"
	bucketByStatusIndex = "chain_commons_tx_intents_by_status"
)

// Manager is the durable transaction state machine.
//
// Construct with New. Call Resume(ctx) once at daemon startup before any
// Submit calls — Resume scans for non-terminal intents and re-enters the
// processing loop for each.
//
// The processing loop (signing, broadcasting, receipt tracking, replacement)
// is intentionally split out into Processor — this scaffolding commit ships
// the persistence + idempotency surface fully tested. The Processor
// implementation lands in a follow-up commit; this Manager invokes
// Processor.Process(intent) when one exists, otherwise leaves intents in
// their current state (useful for tests that drive transitions manually).
type Manager struct {
	cfg     config.TxIntentPolicy
	store   store.Store
	clock   clock.Clock
	logger  logger.Logger
	metrics metrics.Recorder

	// processor, when non-nil, is invoked for every non-terminal intent
	// (post-Submit and post-Resume). Tests may pass nil to drive transitions
	// directly.
	processor Processor

	mu      sync.Mutex
	waiters map[IntentID][]chan TxIntent

	// submitMu serialises the read-then-write in Submit and Adopt. The
	// idempotency key is only a guarantee if two concurrent first
	// submits of the same key cannot both miss the read, both persist,
	// and both dispatch a processor — which would spend the key twice
	// at two nonces. Held for the existence check and the write only;
	// processor dispatch happens after release.
	submitMu sync.Mutex
}

// Processor advances a TxIntent from any non-terminal state toward a
// terminal state. The full implementation handles signing, broadcasting,
// receipt tracking, replacement, and reorg recovery; this scaffolding
// commit defines the interface and defers the impl to a follow-up.
type Processor interface {
	// Process is called for one intent; it should drive the state machine
	// to terminal and return when done. It runs in its own goroutine.
	Process(ctx context.Context, m *Manager, id IntentID)
}

// New constructs a Manager. Returns an error if the store can't be opened
// for the required buckets.
func New(
	cfg config.TxIntentPolicy,
	st store.Store,
	clk clock.Clock,
	log logger.Logger,
	rec metrics.Recorder,
	proc Processor,
) (*Manager, error) {
	if st == nil {
		return nil, fmt.Errorf("txintent: store is required")
	}
	if clk == nil {
		clk = clock.System()
	}
	if rec == nil {
		rec = metrics.NoOp()
	}
	// pre-create buckets so first Submit doesn't have to.
	if _, err := st.Bucket(bucketIntents); err != nil {
		return nil, fmt.Errorf("txintent: open intents bucket: %w", err)
	}
	if _, err := st.Bucket(bucketByStatusIndex); err != nil {
		return nil, fmt.Errorf("txintent: open status-index bucket: %w", err)
	}
	return &Manager{
		cfg:       cfg,
		store:     st,
		clock:     clk,
		logger:    log,
		metrics:   rec,
		processor: proc,
		waiters:   make(map[IntentID][]chan TxIntent),
	}, nil
}

// Submit creates or returns an intent. Idempotency is content-addressed by
// (Kind, KeyParams): submitting the same logical operation twice returns
// the same IntentID, regardless of whether the first call's tx has yet been
// broadcast.
//
// Returns the IntentID. The intent's lifecycle proceeds asynchronously
// from here — callers use Wait or Status to observe progress.
func (m *Manager) Submit(ctx context.Context, p Params) (IntentID, error) {
	if p.Kind == "" {
		return IntentID{}, fmt.Errorf("txintent: Kind is required")
	}
	if p.GasLimit == 0 {
		return IntentID{}, fmt.Errorf("txintent: GasLimit is required (> 0)")
	}

	id := ComputeID(p.Kind, p.KeyParams)
	now := m.clock.Now()

	m.submitMu.Lock()
	defer m.submitMu.Unlock()

	// Check if intent already exists — idempotent submit.
	existing, err := m.read(id)
	if err == nil {
		// Already present; return existing ID without modification.
		if m.logger != nil {
			m.logger.Debug("txintent.submit.idempotent",
				logger.String("id", id.Hex()),
				logger.String("kind", p.Kind),
				logger.String("status", existing.Status.String()),
			)
		}
		m.metrics.CounterAdd("livepeer_chain_txintent_submit_total",
			metrics.Labels{"kind": p.Kind, "outcome": "idempotent"}, 1)
		return id, nil
	} else if err != store.ErrNotFound {
		return IntentID{}, fmt.Errorf("txintent: read existing intent: %w", err)
	}

	value := p.Value
	if value == nil {
		value = new(big.Int)
	}

	intent := TxIntent{
		ID:            id,
		Kind:          p.Kind,
		KeyParams:     append([]byte(nil), p.KeyParams...),
		To:            p.To,
		CallData:      append([]byte(nil), p.CallData...),
		Value:         new(big.Int).Set(value),
		GasLimit:      p.GasLimit,
		Metadata:      p.Metadata,
		Status:        StatusPending,
		Attempts:      nil,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}

	if err := m.write(intent); err != nil {
		return IntentID{}, fmt.Errorf("txintent: persist new intent: %w", err)
	}

	if m.logger != nil {
		m.logger.Info("txintent.submit.new",
			logger.String("id", id.Hex()),
			logger.String("kind", p.Kind),
		)
	}
	m.metrics.CounterAdd("livepeer_chain_txintent_submit_total",
		metrics.Labels{"kind": p.Kind, "outcome": "new"}, 1)

	if m.processor != nil {
		// Processing continues asynchronously after Submit returns, so it
		// must not be tied to short-lived RPC/request cancellation. Preserve
		// context values but detach cancellation/deadlines from the caller.
		go m.processor.Process(context.WithoutCancel(ctx), m, id)
	}

	return id, nil
}

// AdoptOption tunes Adopt.
type AdoptOption func(*adoptOptions)

type adoptOptions struct {
	broadcastedAt time.Time
	feeCap        *big.Int
	tipCap        *big.Int
}

// WithBroadcastedAt records when the adopted transaction was originally
// broadcast. Default: the manager clock's now.
func WithBroadcastedAt(t time.Time) AdoptOption {
	return func(o *adoptOptions) { o.broadcastedAt = t }
}

// WithGasCaps records the fee and tip caps the adopted transaction was
// signed with, so a later replacement bumps from the real values rather
// than from a fresh oracle read.
func WithGasCaps(feeCap, tipCap *big.Int) AdoptOption {
	return func(o *adoptOptions) {
		o.feeCap = copyBig(feeCap)
		o.tipCap = copyBig(tipCap)
	}
}

// Adopt records a transaction that was broadcast by something other than
// this Manager — typically the implementation this daemon replaced — as an
// intent already in StatusSubmitted with one attempt (txHash, nonce), and
// hands it to the Processor to track to confirmation. Nothing is signed or
// sent: the tx is already on the network. It exists so an upgrade can take
// over in-flight work instead of re-sending it.
//
// Idempotency is exactly Submit's: the same (Kind, KeyParams) adopted or
// submitted twice returns the existing IntentID without modification, so
// an Adopt after a Submit (or vice versa) never yields two intents.
//
// p.To, p.CallData, p.Value and p.GasLimit describe the transaction as best
// the caller knows it; they are only used if the adopted attempt stalls and
// the Processor has to sign a replacement at the same nonce. Pass
// WithGasCaps when the original caps are known; otherwise a replacement
// starts from the gas oracle's current suggestion.
func (m *Manager) Adopt(ctx context.Context, p Params, txHash chain.TxHash, nonce uint64, opts ...AdoptOption) (IntentID, error) {
	if p.Kind == "" {
		return IntentID{}, fmt.Errorf("txintent: Kind is required")
	}
	if p.GasLimit == 0 {
		return IntentID{}, fmt.Errorf("txintent: GasLimit is required (> 0)")
	}
	if txHash == (chain.TxHash{}) {
		return IntentID{}, fmt.Errorf("txintent: Adopt requires a non-zero tx hash")
	}

	id := ComputeID(p.Kind, p.KeyParams)
	now := m.clock.Now()
	o := adoptOptions{broadcastedAt: now}
	for _, opt := range opts {
		opt(&o)
	}

	m.submitMu.Lock()
	defer m.submitMu.Unlock()

	existing, err := m.read(id)
	if err == nil {
		if m.logger != nil {
			m.logger.Debug("txintent.adopt.idempotent",
				logger.String("id", id.Hex()),
				logger.String("kind", p.Kind),
				logger.String("status", existing.Status.String()),
			)
		}
		m.metrics.CounterAdd("livepeer_chain_txintent_submit_total",
			metrics.Labels{"kind": p.Kind, "outcome": "idempotent"}, 1)
		return id, nil
	} else if err != store.ErrNotFound {
		return IntentID{}, fmt.Errorf("txintent: read existing intent: %w", err)
	}

	value := p.Value
	if value == nil {
		value = new(big.Int)
	}
	intent := TxIntent{
		ID:        id,
		Kind:      p.Kind,
		KeyParams: append([]byte(nil), p.KeyParams...),
		To:        p.To,
		CallData:  append([]byte(nil), p.CallData...),
		Value:     new(big.Int).Set(value),
		GasLimit:  p.GasLimit,
		Metadata:  p.Metadata,
		Status:    StatusSubmitted,
		Attempts: []IntentAttempt{{
			Nonce:         nonce,
			GasFeeCap:     o.feeCap,
			GasTipCap:     o.tipCap,
			SignedTxHash:  txHash,
			BroadcastedAt: o.broadcastedAt,
		}},
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	if err := m.write(intent); err != nil {
		return IntentID{}, fmt.Errorf("txintent: persist adopted intent: %w", err)
	}

	if m.logger != nil {
		m.logger.Info("txintent.adopt.new",
			logger.String("id", id.Hex()),
			logger.String("kind", p.Kind),
			logger.String("tx", txHash.Hex()),
			logger.Uint64("nonce", nonce),
		)
	}
	m.metrics.CounterAdd("livepeer_chain_txintent_submit_total",
		metrics.Labels{"kind": p.Kind, "outcome": "adopted"}, 1)

	if m.processor != nil {
		go m.processor.Process(context.WithoutCancel(ctx), m, id)
	}
	return id, nil
}

// Status returns the current intent record.
func (m *Manager) Status(_ context.Context, id IntentID) (TxIntent, error) {
	return m.read(id)
}

// Resubmit re-drives a terminally-FAILED intent: it refreshes the calldata
// (hints may have drifted since the original attempt), resets the intent to
// StatusPending, clears the failure, and re-dispatches it to the Processor —
// which signs and broadcasts a brand-new transaction at a fresh nonce.
//
// This is the sanctioned operator-override of the (Kind, KeyParams)
// idempotency guard: a normal Submit returns the existing failed intent
// without retrying, so a transient revert (e.g. an uninitialized round) would
// otherwise strand that intent forever. Resubmit deliberately mutates a
// terminal state, so it is restricted to StatusFailed — a Confirmed intent is
// never re-driven (its success is immutable) and a non-terminal intent is
// already in flight; both return an error so callers can surface a clear
// message instead of double-broadcasting.
func (m *Manager) Resubmit(ctx context.Context, id IntentID, calldata []byte) error {
	if err := m.transition(id, func(t *TxIntent) error {
		if t.Status != StatusFailed {
			return fmt.Errorf("txintent: Resubmit requires StatusFailed, intent is %s", t.Status)
		}
		t.CallData = append([]byte(nil), calldata...)
		t.Status = StatusPending
		t.FailedReason = nil
		t.LastUpdatedAt = m.clock.Now()
		return nil
	}); err != nil {
		return err
	}
	if m.logger != nil {
		m.logger.Info("txintent.resubmit", logger.String("id", id.Hex()))
	}
	m.metrics.CounterAdd("livepeer_chain_txintent_submit_total",
		metrics.Labels{"kind": "", "outcome": "resubmit"}, 1)
	if m.processor != nil {
		go m.processor.Process(context.WithoutCancel(ctx), m, id)
	}
	return nil
}

// Wait blocks until the intent reaches a terminal state, ctx is cancelled,
// or the intent doesn't exist (returns ErrNotFound).
func (m *Manager) Wait(ctx context.Context, id IntentID) (TxIntent, error) {
	// Fast path: already terminal.
	cur, err := m.read(id)
	if err != nil {
		return TxIntent{}, err
	}
	if cur.Status.IsTerminal() {
		return cur, nil
	}

	ch := make(chan TxIntent, 1)
	m.mu.Lock()
	m.waiters[id] = append(m.waiters[id], ch)
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		ws := m.waiters[id]
		for i, w := range ws {
			if w == ch {
				m.waiters[id] = append(ws[:i], ws[i+1:]...)
				break
			}
		}
		if len(m.waiters[id]) == 0 {
			delete(m.waiters, id)
		}
		m.mu.Unlock()
	}()

	select {
	case t := <-ch:
		return t, nil
	case <-ctx.Done():
		return TxIntent{}, ctx.Err()
	}
}

// List returns all intents matching the filter, ordered by CreatedAt asc.
func (m *Manager) List(_ context.Context, filter Filter) ([]TxIntent, error) {
	bucket, err := m.store.Bucket(bucketIntents)
	if err != nil {
		return nil, err
	}
	var out []TxIntent
	err = bucket.ForEach(func(_, value []byte) error {
		var t TxIntent
		if err := decodeIntent(value, &t); err != nil {
			return err
		}
		if !matchFilter(t, filter) {
			return nil
		}
		out = append(out, t)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// CreatedAt asc
	sortByCreatedAt(out)
	return out, nil
}

// Resume scans the store for non-terminal intents and dispatches them to
// the Processor. Call once at daemon startup before any Submit. Idempotent:
// calling multiple times is safe.
func (m *Manager) Resume(ctx context.Context) error {
	if m.processor == nil {
		return nil
	}
	intents, err := m.List(ctx, Filter{
		Statuses: []IntentStatus{
			StatusPending, StatusSigned, StatusSubmitted, StatusMined, StatusReplaced,
		},
	})
	if err != nil {
		return err
	}
	if m.logger != nil {
		m.logger.Info("txintent.resume", logger.Int("non_terminal", len(intents)))
	}
	for _, t := range intents {
		go m.processor.Process(ctx, m, t.ID)
	}
	return nil
}

// MarkConfirmed transitions an intent from StatusMined to StatusConfirmed.
// Used by Processor implementations; tests use it to drive the state
// machine deterministically.
func (m *Manager) MarkConfirmed(id IntentID) error {
	return m.transition(id, func(t *TxIntent) error {
		if t.Status != StatusMined && t.Status != StatusSubmitted {
			return fmt.Errorf("MarkConfirmed: invalid prior status %s", t.Status)
		}
		t.Status = StatusConfirmed
		now := m.clock.Now()
		t.ConfirmedAt = &now
		t.LastUpdatedAt = now
		return nil
	})
}

// MarkFailed transitions an intent to StatusFailed with a classified reason.
// Used by Processor implementations; tests use it to drive the state machine.
func (m *Manager) MarkFailed(id IntentID, reason *cerrors.Error) error {
	return m.transition(id, func(t *TxIntent) error {
		if t.Status.IsTerminal() {
			return fmt.Errorf("MarkFailed: intent already terminal (%s)", t.Status)
		}
		t.Status = StatusFailed
		t.FailedReason = reason
		t.LastUpdatedAt = m.clock.Now()
		return nil
	})
}

// SetStatus is a low-level transition for the Processor and tests. It
// rejects backwards transitions out of terminal states (those are immutable).
func (m *Manager) SetStatus(id IntentID, s IntentStatus) error {
	return m.transition(id, func(t *TxIntent) error {
		if t.Status.IsTerminal() {
			return fmt.Errorf("SetStatus: intent already terminal (%s)", t.Status)
		}
		t.Status = s
		t.LastUpdatedAt = m.clock.Now()
		return nil
	})
}

// AppendAttempt records a new IntentAttempt on the intent and (optionally)
// updates the status. Used by Processor implementations.
func (m *Manager) AppendAttempt(id IntentID, attempt IntentAttempt, newStatus IntentStatus) error {
	return m.transition(id, func(t *TxIntent) error {
		if t.Status.IsTerminal() {
			return fmt.Errorf("AppendAttempt: intent already terminal (%s)", t.Status)
		}
		// Mark prior attempts as replaced (their ReplacedAt is set when
		// the new attempt has the same Nonce — this is a gas bump).
		if cur := t.CurrentAttempt(); cur != nil && cur.Nonce == attempt.Nonce {
			now := m.clock.Now()
			cur.ReplacedAt = &now
		}
		t.Attempts = append(t.Attempts, attempt)
		t.Status = newStatus
		t.LastUpdatedAt = m.clock.Now()
		return nil
	})
}

// transition is the workhorse for state-changing operations. Reads the
// current intent, applies fn, persists, and notifies waiters if the new
// status is terminal.
func (m *Manager) transition(id IntentID, fn func(*TxIntent) error) error {
	t, err := m.read(id)
	if err != nil {
		return err
	}
	if err := fn(&t); err != nil {
		return err
	}
	if err := m.write(t); err != nil {
		return err
	}
	if t.Status.IsTerminal() {
		m.notifyWaiters(t)
	}
	return nil
}

func (m *Manager) notifyWaiters(t TxIntent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws := m.waiters[t.ID]
	for _, w := range ws {
		// Non-blocking send — buffered channel; if buffer full, drop.
		select {
		case w <- t:
		default:
		}
	}
}

// read returns the persisted TxIntent for id, or store.ErrNotFound.
func (m *Manager) read(id IntentID) (TxIntent, error) {
	bucket, err := m.store.Bucket(bucketIntents)
	if err != nil {
		return TxIntent{}, err
	}
	value, err := bucket.Get(id[:])
	if err != nil {
		return TxIntent{}, err
	}
	var t TxIntent
	if err := decodeIntent(value, &t); err != nil {
		return TxIntent{}, err
	}
	return t, nil
}

// write persists the TxIntent.
func (m *Manager) write(t TxIntent) error {
	bucket, err := m.store.Bucket(bucketIntents)
	if err != nil {
		return err
	}
	encoded, err := encodeIntent(t)
	if err != nil {
		return err
	}
	return bucket.Put(t.ID[:], encoded)
}

// Encode/decode helpers — msgpack chosen for size + schema-tolerance.

type encodedAttempt struct {
	Nonce         uint64
	GasFeeCap     []byte // big.Int.Bytes() — empty for nil
	GasTipCap     []byte
	SignedTxHash  []byte
	BroadcastedAt int64 // unix nano
	MinedBlock    *uint64
	MinedBlockHsh []byte
	ReceiptStatus *uint64
	ReplacedAt    int64 // unix nano; 0 means nil
}

type encodedIntent struct {
	ID            [32]byte
	Kind          string
	KeyParams     []byte
	To            [20]byte
	CallData      []byte
	Value         []byte
	GasLimit      uint64
	Metadata      map[string]string
	Status        uint8
	Attempts      []encodedAttempt
	CreatedAt     int64
	LastUpdatedAt int64
	ConfirmedAt   int64
	FailedClass   *uint8
	FailedCode    string
	FailedMsg     string
}

func encodeIntent(t TxIntent) ([]byte, error) {
	enc := encodedIntent{
		ID:            t.ID,
		Kind:          t.Kind,
		KeyParams:     t.KeyParams,
		To:            t.To,
		CallData:      t.CallData,
		Value:         valueBytes(t.Value),
		GasLimit:      t.GasLimit,
		Metadata:      t.Metadata,
		Status:        uint8(t.Status),
		CreatedAt:     t.CreatedAt.UnixNano(),
		LastUpdatedAt: t.LastUpdatedAt.UnixNano(),
	}
	if t.ConfirmedAt != nil {
		enc.ConfirmedAt = t.ConfirmedAt.UnixNano()
	}
	if t.FailedReason != nil {
		c := uint8(t.FailedReason.Class)
		enc.FailedClass = &c
		enc.FailedCode = t.FailedReason.Code
		enc.FailedMsg = t.FailedReason.Msg
	}
	for _, a := range t.Attempts {
		ea := encodedAttempt{
			Nonce:         a.Nonce,
			GasFeeCap:     valueBytes(a.GasFeeCap),
			GasTipCap:     valueBytes(a.GasTipCap),
			SignedTxHash:  a.SignedTxHash[:],
			BroadcastedAt: a.BroadcastedAt.UnixNano(),
		}
		if a.MinedBlock != nil {
			mb := uint64(*a.MinedBlock)
			ea.MinedBlock = &mb
		}
		if a.MinedBlockHash != nil {
			ea.MinedBlockHsh = (*a.MinedBlockHash)[:]
		}
		if a.ReceiptStatus != nil {
			rs := *a.ReceiptStatus
			ea.ReceiptStatus = &rs
		}
		if a.ReplacedAt != nil {
			ea.ReplacedAt = a.ReplacedAt.UnixNano()
		}
		enc.Attempts = append(enc.Attempts, ea)
	}
	return msgpack.Marshal(enc)
}

func decodeIntent(b []byte, t *TxIntent) error {
	var enc encodedIntent
	if err := msgpack.Unmarshal(b, &enc); err != nil {
		return err
	}
	t.ID = enc.ID
	t.Kind = enc.Kind
	t.KeyParams = enc.KeyParams
	t.To = enc.To
	t.CallData = enc.CallData
	t.Value = bytesValue(enc.Value)
	t.GasLimit = enc.GasLimit
	t.Metadata = enc.Metadata
	t.Status = IntentStatus(enc.Status)
	t.CreatedAt = time.Unix(0, enc.CreatedAt)
	t.LastUpdatedAt = time.Unix(0, enc.LastUpdatedAt)
	if enc.ConfirmedAt != 0 {
		ca := time.Unix(0, enc.ConfirmedAt)
		t.ConfirmedAt = &ca
	}
	if enc.FailedClass != nil {
		t.FailedReason = &cerrors.Error{
			Class: cerrors.ErrorClass(*enc.FailedClass),
			Code:  enc.FailedCode,
			Msg:   enc.FailedMsg,
		}
	}
	for _, ea := range enc.Attempts {
		var sth chain.TxHash
		copy(sth[:], ea.SignedTxHash)
		a := IntentAttempt{
			Nonce:         ea.Nonce,
			GasFeeCap:     bytesValue(ea.GasFeeCap),
			GasTipCap:     bytesValue(ea.GasTipCap),
			SignedTxHash:  sth,
			BroadcastedAt: time.Unix(0, ea.BroadcastedAt),
		}
		if ea.MinedBlock != nil {
			mb := chain.BlockNumber(*ea.MinedBlock)
			a.MinedBlock = &mb
		}
		if ea.MinedBlockHsh != nil {
			var h chain.TxHash
			copy(h[:], ea.MinedBlockHsh)
			a.MinedBlockHash = &h
		}
		if ea.ReceiptStatus != nil {
			rs := *ea.ReceiptStatus
			a.ReceiptStatus = &rs
		}
		if ea.ReplacedAt != 0 {
			ra := time.Unix(0, ea.ReplacedAt)
			a.ReplacedAt = &ra
		}
		t.Attempts = append(t.Attempts, a)
	}
	return nil
}

func valueBytes(b *big.Int) []byte {
	if b == nil {
		return nil
	}
	return b.Bytes()
}

func bytesValue(b []byte) *big.Int {
	if b == nil {
		return new(big.Int)
	}
	return new(big.Int).SetBytes(b)
}

func matchFilter(t TxIntent, f Filter) bool {
	if len(f.Kinds) > 0 {
		match := false
		for _, k := range f.Kinds {
			if t.Kind == k {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}
	if len(f.Statuses) > 0 {
		match := false
		for _, s := range f.Statuses {
			if t.Status == s {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}
	if f.Since != nil && t.CreatedAt.Before(*f.Since) {
		return false
	}
	return true
}

func sortByCreatedAt(in []TxIntent) {
	for i := 1; i < len(in); i++ {
		j := i
		for j > 0 && in[j-1].CreatedAt.After(in[j].CreatedAt) {
			in[j-1], in[j] = in[j], in[j-1]
			j--
		}
	}
}
