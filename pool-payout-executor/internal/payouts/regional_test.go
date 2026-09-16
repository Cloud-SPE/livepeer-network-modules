package payouts_test

import (
	"context"
	"errors"
	"math/big"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing/simchain"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/payouts"
)

func TestRegionalIdentityFencesRestartBeforeResumeAndPreservesFullMemberValue(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	cfg := config.Executor{PoolID: "pool_us", ExpectedWalletAddress: f.acct.Address.Hex(), ChainID: uint64(simchain.DevChainID), ConfirmationBlocks: 2, IntentStorePath: filepath.Join(t.TempDir(), "payout-intents.db")}
	open := func(c config.Executor) (*payouts.Engine, error) {
		return payouts.OpenWith(ctx, c, f.rpc, f.acct.Keystore, simchain.DevChainID, f.opts)
	}
	e, err := open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	to := f.sim.Accounts[1].Address
	memberBefore, _ := f.rpc.BalanceAt(ctx, to, nil)
	operatorBefore, _ := f.rpc.BalanceAt(ctx, f.acct.Address, nil)
	sent, err := e.Dispatch(ctx, "regional-intent", to, big.NewInt(1000))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	broadcasts := f.rpc.Calls("SendTransaction")
	for _, mutate := range []func(*config.Executor){
		func(c *config.Executor) { c.PoolID = "pool_eu" }, func(c *config.Executor) { c.PoolID = "" }, func(c *config.Executor) { c.ExpectedWalletAddress = f.sim.Accounts[1].Address.Hex() }, func(c *config.Executor) { c.ExpectedWalletAddress = "" }, func(c *config.Executor) { c.ChainID++ },
	} {
		bad := cfg
		mutate(&bad)
		if rejected, err := open(bad); err == nil {
			_ = rejected.Close()
			t.Fatalf("wrong regional authority opened %+v", bad)
		}
		if f.rpc.Calls("SendTransaction") != broadcasts {
			t.Fatal("wrong identity resumed signing")
		}
	}
	e, err = open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	again, err := e.Dispatch(ctx, "regional-intent", to, big.NewInt(1000))
	if err != nil || !again.Reused || again.TxHash != sent.TxHash {
		t.Fatalf("restart replay %+v %v", again, err)
	}
	if _, err := e.Dispatch(ctx, "regional-intent", to, big.NewInt(999)); err == nil {
		t.Fatal("same id accepted different amount")
	}
	if _, err := e.Dispatch(ctx, "regional-intent", f.sim.Accounts[0].Address, big.NewInt(1000)); err == nil {
		t.Fatal("same id accepted different destination")
	}
	f.mine()
	outcome, err := e.Outcome(ctx, "regional-intent")
	if err != nil || outcome.State != payouts.StatePaid {
		t.Fatalf("outcome %+v %v", outcome, err)
	}
	memberAfter, _ := f.rpc.BalanceAt(ctx, to, nil)
	operatorAfter, _ := f.rpc.BalanceAt(ctx, f.acct.Address, nil)
	if new(big.Int).Sub(memberAfter, memberBefore).Cmp(big.NewInt(1000)) != 0 {
		t.Fatal("member amount reduced by fees")
	}
	if new(big.Int).Sub(operatorBefore, operatorAfter).Cmp(big.NewInt(1000)) <= 0 {
		t.Fatal("operator did not cover payout gas")
	}
}
func TestRegionalTrackingRejectsUnknownOrUnrelatedTransaction(t *testing.T) {
	f := newFixture(t)
	f.cfg.PoolID = "pool_us"
	f.cfg.ExpectedWalletAddress = f.acct.Address.Hex()
	e := f.open()
	ctx := ctxT(t)
	to := f.sim.Accounts[0].Address
	if _, err := e.Track(ctx, "unknown", "0x"+strings.Repeat("a", 64), "nonce-1", to, big.NewInt(7)); !errors.Is(err, payouts.ErrUnknownTx) {
		t.Fatalf("unproven nonce hint adopted: %v", err)
	}
	sender := f.sim.Accounts[1]
	tx, err := f.sim.NewDynamicFeeTx(ctx, sender, &to, big.NewInt(7), 21000, nil)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := f.sim.SignTx(sender, tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.sim.RPC().SendTransaction(ctx, signed); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Track(ctx, "unrelated", signed.Hash().Hex(), "", to, big.NewInt(7)); err == nil {
		t.Fatal("another wallet's transaction satisfied regional payout")
	}
}
func TestRegionalIdentityCannotBeGuessedForExistingLegacyIntents(t *testing.T) {
	f := newFixture(t)
	e := f.open()
	ctx := ctxT(t)
	if _, err := e.Dispatch(ctx, "legacy", f.sim.Accounts[1].Address, big.NewInt(1)); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	f.cfg.PoolID = "pool_us"
	f.cfg.ExpectedWalletAddress = f.acct.Address.Hex()
	opts := f.opts
	opts.Store = f.store
	if got, err := payouts.OpenWith(context.Background(), f.cfg, f.rpc, f.acct.Keystore, simchain.DevChainID, opts); err == nil {
		_ = got.Close()
		t.Fatal("legacy transaction history silently assigned a regional identity")
	}
}
