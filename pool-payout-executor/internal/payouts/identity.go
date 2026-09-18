package payouts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/config"
	"github.com/ethereum/go-ethereum/common"
)

type regionalIdentity struct {
	PoolID  string `json:"pool_id"`
	Wallet  string `json:"wallet"`
	ChainID uint64 `json:"chain_id"`
}

// Bind before Resume can start a signing goroutine. The identity lives in the
// same durable store as transaction intents, so restoring it restores the fence.
func bindRegionalIdentity(ctx context.Context, st store.Store, mgr *txintent.Manager, cfg config.Executor, wallet common.Address, chainID chain.ChainID) error {
	if cfg.ExpectedWalletAddress != "" && (!common.IsHexAddress(cfg.ExpectedWalletAddress) || common.HexToAddress(cfg.ExpectedWalletAddress) != wallet) {
		return fmt.Errorf("payout wallet does not match expected_wallet_address")
	}
	if cfg.PoolID != "" && (cfg.ExpectedWalletAddress == "" || wallet == (common.Address{})) {
		return fmt.Errorf("regional payout requires its dedicated expected wallet")
	}
	if cfg.PoolID != "" && cfg.ChainID != 0 && uint64(chainID) != cfg.ChainID {
		return fmt.Errorf("payout chain does not match configured chain")
	}
	intents, err := mgr.List(ctx, txintent.Filter{})
	if err != nil {
		return err
	}
	expected := regionalIdentity{PoolID: cfg.PoolID, Wallet: strings.ToLower(wallet.Hex()), ChainID: uint64(chainID)}
	return st.Update(func(tx store.Tx) error {
		b, err := tx.Bucket("regional_payout_identity")
		if err != nil {
			return err
		}
		raw, err := b.Get([]byte("identity"))
		if err == nil {
			var prior regionalIdentity
			if err := json.Unmarshal(raw, &prior); err != nil {
				return fmt.Errorf("corrupt regional payout identity: %w", err)
			}
			if prior != expected {
				return fmt.Errorf("regional payout store pool, wallet and chain are immutable")
			}
			return nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if cfg.PoolID == "" {
			return nil
		}
		if len(intents) != 0 {
			return fmt.Errorf("cannot assign a regional identity to existing unqualified payout intents")
		}
		raw, err = json.Marshal(expected)
		if err != nil {
			return err
		}
		return b.Put([]byte("identity"), raw)
	})
}
