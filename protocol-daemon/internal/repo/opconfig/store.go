// Package opconfig persists the daemon's runtime OperationalConfig in the
// shared BoltDB store.
//
// The config is a single JSON record under a fixed key. Load returns the
// first-boot defaults (reward on, everything else off) when no record
// exists, so a fresh daemon is always usable. Save validates and
// normalizes before writing, so an invalid policy can never be persisted.
//
// All persistence goes through chain-commons.providers.store — never raw
// bbolt.
package opconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/types"
	"github.com/ethereum/go-ethereum/common"
)

// bucketName is the BoltDB bucket holding the operational config record.
const bucketName = "protocol_daemon_op_config"

// configKey is the single key under which the JSON record is stored.
var configKey = []byte("config")

// Store reads and writes the daemon's OperationalConfig. Safe for
// concurrent use; an in-memory copy is kept so reads on the hot path
// (per-round automation decisions) don't hit BoltDB every time.
type Store struct {
	store store.Store

	mu     sync.RWMutex
	cached types.OperationalConfig
}

// New constructs a Store and loads the current config (or defaults) into
// the in-memory cache.
func New(s store.Store) (*Store, error) {
	if s == nil {
		return nil, errors.New("opconfig: store is required")
	}
	if _, err := s.Bucket(bucketName); err != nil {
		return nil, fmt.Errorf("opconfig: open bucket: %w", err)
	}
	st := &Store{store: s}
	cfg, err := st.loadFromStore()
	if err != nil {
		return nil, err
	}
	st.cached = cfg
	return st, nil
}

// Get returns the current operational config. Returns the cached copy;
// wei pointers are deep-copied so callers can't mutate stored state.
func (s *Store) Get() types.OperationalConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConfig(s.cached)
}

// Set validates, normalizes, persists, and updates the cache. Returns the
// stored (normalized) config on success.
func (s *Store) Set(cfg types.OperationalConfig) (types.OperationalConfig, error) {
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return types.OperationalConfig{}, err
	}
	bucket, err := s.store.Bucket(bucketName)
	if err != nil {
		return types.OperationalConfig{}, err
	}
	encoded, err := json.Marshal(toRecord(cfg))
	if err != nil {
		return types.OperationalConfig{}, fmt.Errorf("opconfig: marshal: %w", err)
	}
	if err := bucket.Put(configKey, encoded); err != nil {
		return types.OperationalConfig{}, err
	}
	s.mu.Lock()
	s.cached = cloneConfig(cfg)
	s.mu.Unlock()
	return cloneConfig(cfg), nil
}

// loadFromStore reads the persisted record, returning defaults on miss.
func (s *Store) loadFromStore() (types.OperationalConfig, error) {
	bucket, err := s.store.Bucket(bucketName)
	if err != nil {
		return types.OperationalConfig{}, err
	}
	value, err := bucket.Get(configKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return types.DefaultOperationalConfig(), nil
		}
		return types.OperationalConfig{}, err
	}
	var rec record
	if err := json.Unmarshal(value, &rec); err != nil {
		return types.OperationalConfig{}, fmt.Errorf("opconfig: unmarshal: %w", err)
	}
	cfg := rec.toConfig()
	cfg.Normalize()
	return cfg, nil
}

// record is the on-disk JSON shape. Wei values are stored as decimal
// strings (explicit, human-readable in the BoltDB file, and avoids any
// ambiguity around big-number JSON encoding); addresses as hex.
type record struct {
	RoundInitEnabled     bool   `json:"round_init_enabled"`
	RewardEnabled        bool   `json:"reward_enabled"`
	RewardBeforeTransfer bool   `json:"reward_before_transfer"`
	TransferEnabled      bool   `json:"transfer_enabled"`
	TransferReceiver     string `json:"transfer_receiver"`
	TransferMinRetainWei string `json:"transfer_min_retain_wei"`
	WithdrawEnabled      bool   `json:"withdraw_enabled"`
	WithdrawReceiver     string `json:"withdraw_receiver"`
	WithdrawThresholdWei string `json:"withdraw_threshold_wei"`
}

func toRecord(c types.OperationalConfig) record {
	return record{
		RoundInitEnabled:     c.RoundInitEnabled,
		RewardEnabled:        c.RewardEnabled,
		RewardBeforeTransfer: c.RewardBeforeTransfer,
		TransferEnabled:      c.TransferBond.Enabled,
		TransferReceiver:     c.TransferBond.Receiver.Hex(),
		TransferMinRetainWei: weiString(c.TransferBond.MinRetainWei),
		WithdrawEnabled:      c.WithdrawFees.Enabled,
		WithdrawReceiver:     c.WithdrawFees.Receiver.Hex(),
		WithdrawThresholdWei: weiString(c.WithdrawFees.ThresholdWei),
	}
}

func (r record) toConfig() types.OperationalConfig {
	return types.OperationalConfig{
		RoundInitEnabled:     r.RoundInitEnabled,
		RewardEnabled:        r.RewardEnabled,
		RewardBeforeTransfer: r.RewardBeforeTransfer,
		TransferBond: types.TransferBondConfig{
			Enabled:      r.TransferEnabled,
			Receiver:     parseAddr(r.TransferReceiver),
			MinRetainWei: parseWei(r.TransferMinRetainWei),
		},
		WithdrawFees: types.WithdrawFeesConfig{
			Enabled:      r.WithdrawEnabled,
			Receiver:     parseAddr(r.WithdrawReceiver),
			ThresholdWei: parseWei(r.WithdrawThresholdWei),
		},
	}
}

func weiString(w chain.Wei) string {
	if w == nil {
		return "0"
	}
	return w.String()
}

func parseWei(s string) chain.Wei {
	if s == "" {
		return new(big.Int)
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return new(big.Int)
	}
	return v
}

func parseAddr(s string) chain.Address {
	if s == "" {
		return chain.Address{}
	}
	return common.HexToAddress(s)
}

func cloneConfig(c types.OperationalConfig) types.OperationalConfig {
	out := c
	out.TransferBond.MinRetainWei = cloneWei(c.TransferBond.MinRetainWei)
	out.WithdrawFees.ThresholdWei = cloneWei(c.WithdrawFees.ThresholdWei)
	return out
}

func cloneWei(w chain.Wei) chain.Wei {
	if w == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(w)
}
