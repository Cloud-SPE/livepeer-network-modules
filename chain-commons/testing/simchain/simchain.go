// Package simchain exposes go-ethereum's in-process simulated backend as a
// chain-commons rpc.RPC, so daemon tests can run real signed transactions,
// nonces, receipts, replacement and restart-resume without a network.
//
// It is the second of the three test layers in plan 0048 §3: above the
// programmable fakes in the parent package (which answer whatever a test
// tells them to) and below the real-process integration stack. Everything
// here is deterministic: accounts derive from seeds, blocks are mined only
// when a test asks, and the chain id is go-ethereum's dev chain (1337).
//
// Typical use:
//
//	sim := simchain.New(t)
//	acct := sim.Accounts[0]                // funded, with a keystore.Keystore
//	rpc := sim.RPC()                       // rpc.RPC over the simulated node
//	tx, _ := sim.SendValue(ctx, acct, to, big.NewInt(1))
//	sim.Commit()                           // mine it
//	receipt, _ := rpc.TransactionReceipt(ctx, tx.Hash())
//
// Accounts are keyed by seed through the parent package's FakeKeystore, so
// chaintest.NewFakeKeystore(acct.Seed) yields the same signer a consumer
// daemon would wire into its processor.
package simchain

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/rpc"
	chaintest "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/ethereum/go-ethereum/params"
)

// DevChainID is the chain id of go-ethereum's simulated backend.
const DevChainID chain.ChainID = 1337

// DefaultBalance is what each account is funded with at genesis: 1000 ETH.
var DefaultBalance = new(big.Int).Mul(big.NewInt(1000), big.NewInt(params.Ether))

// Account is a funded genesis account. Keystore signs for it; Seed is the
// string it derives from, so a consumer can rebuild the same signer with
// chaintest.NewFakeKeystore(Seed).
type Account struct {
	Seed     string
	Address  chain.Address
	Keystore *chaintest.FakeKeystore
	key      *ecdsa.PrivateKey
}

// Chain owns one simulated backend for the lifetime of a test.
type Chain struct {
	backend  *simulated.Backend
	client   simulated.Client
	Accounts []*Account

	mu sync.Mutex
}

type options struct {
	seeds   []string
	balance *big.Int
}

// Option configures New.
type Option func(*options)

// WithAccounts funds n accounts, seeded "simchain-account-<i>". Default 2.
func WithAccounts(n int) Option {
	return func(o *options) {
		o.seeds = o.seeds[:0]
		for i := 0; i < n; i++ {
			o.seeds = append(o.seeds, fmt.Sprintf("simchain-account-%d", i))
		}
	}
}

// WithSeeds funds one account per seed, in order.
func WithSeeds(seeds ...string) Option {
	return func(o *options) { o.seeds = append([]string(nil), seeds...) }
}

// WithBalance sets the genesis balance of every account.
func WithBalance(wei *big.Int) Option {
	return func(o *options) { o.balance = new(big.Int).Set(wei) }
}

// New starts a simulated chain and registers Close on t.Cleanup.
func New(t testing.TB, opts ...Option) *Chain {
	t.Helper()
	o := options{balance: DefaultBalance}
	WithAccounts(2)(&o)
	for _, opt := range opts {
		opt(&o)
	}
	if len(o.seeds) == 0 {
		t.Fatal("simchain: at least one account is required")
	}

	c := &Chain{}
	alloc := types.GenesisAlloc{}
	for _, seed := range o.seeds {
		h := sha256.Sum256([]byte(seed))
		key, err := crypto.ToECDSA(h[:])
		if err != nil {
			t.Fatalf("simchain: derive key for seed %q: %v", seed, err)
		}
		acct := &Account{
			Seed:     seed,
			Address:  crypto.PubkeyToAddress(key.PublicKey),
			Keystore: chaintest.NewFakeKeystore(seed),
			key:      key,
		}
		if acct.Keystore.Address() != acct.Address {
			t.Fatalf("simchain: keystore/seed derivation drifted for %q", seed)
		}
		alloc[acct.Address] = types.Account{Balance: new(big.Int).Set(o.balance)}
		c.Accounts = append(c.Accounts, acct)
	}

	c.backend = simulated.NewBackend(alloc)
	c.client = c.backend.Client()
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// Close stops the backend. Safe to call more than once.
func (c *Chain) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.backend == nil {
		return nil
	}
	err := c.backend.Close()
	c.backend = nil
	return err
}

// ChainID is always DevChainID.
func (c *Chain) ChainID() chain.ChainID { return DevChainID }

// RPC returns the rpc.RPC view of the chain. Its Close is a no-op; the
// Chain owns the backend.
func (c *Chain) RPC() *RPC { return &RPC{c: c.client} }

// Commit mines one block containing every pending transaction and returns
// its hash.
func (c *Chain) Commit() common.Hash {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.backend.Commit()
}

// Mine commits n blocks.
func (c *Chain) Mine(n int) {
	for i := 0; i < n; i++ {
		c.Commit()
	}
}

// MineUntil commits blocks (at most max) until stop returns true. Use it
// to drive a confirmation wait: `go sim.MineUntil(ctx, ...)`. It stops
// when ctx ends.
func (c *Chain) MineUntil(ctx context.Context, max int, stop func() bool) {
	for i := 0; i < max; i++ {
		if ctx.Err() != nil || stop() {
			return
		}
		c.Commit()
	}
}

// SignTx signs tx for acct on the dev chain.
func (c *Chain) SignTx(acct *Account, tx *types.Transaction) (*types.Transaction, error) {
	return acct.Keystore.SignTx(tx, DevChainID)
}

// NewDynamicFeeTx builds an unsigned EIP-1559 tx from acct at its next
// pending nonce with gas caps read from the node.
func (c *Chain) NewDynamicFeeTx(ctx context.Context, acct *Account, to *chain.Address, value *big.Int, gas uint64, data []byte) (*types.Transaction, error) {
	nonce, err := c.client.PendingNonceAt(ctx, acct.Address)
	if err != nil {
		return nil, fmt.Errorf("simchain: pending nonce: %w", err)
	}
	tip, err := c.client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("simchain: tip cap: %w", err)
	}
	head, err := c.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("simchain: head: %w", err)
	}
	feeCap := new(big.Int).Add(tip, new(big.Int).Mul(head.BaseFee, big.NewInt(2)))
	if value == nil {
		value = new(big.Int)
	}
	return types.NewTx(&types.DynamicFeeTx{
		ChainID:   DevChainID.BigInt(),
		Nonce:     nonce,
		GasTipCap: tip,
		GasFeeCap: feeCap,
		Gas:       gas,
		To:        to,
		Value:     value,
		Data:      data,
	}), nil
}

// SendValue signs and broadcasts a plain value transfer. It does not mine;
// call Commit to include it.
func (c *Chain) SendValue(ctx context.Context, from *Account, to chain.Address, wei *big.Int) (*types.Transaction, error) {
	tx, err := c.NewDynamicFeeTx(ctx, from, &to, wei, params.TxGas, nil)
	if err != nil {
		return nil, err
	}
	signed, err := c.SignTx(from, tx)
	if err != nil {
		return nil, err
	}
	if err := c.client.SendTransaction(ctx, signed); err != nil {
		return nil, fmt.Errorf("simchain: send: %w", err)
	}
	return signed, nil
}

// Deploy creates a contract from code (init bytecode). When abiJSON is
// non-empty, args are ABI-packed as constructor arguments and appended.
// The creating transaction is mined before Deploy returns.
func (c *Chain) Deploy(ctx context.Context, from *Account, code []byte, abiJSON string, args ...any) (chain.Address, *types.Receipt, error) {
	data := append([]byte(nil), code...)
	if abiJSON != "" {
		parsed, err := abi.JSON(strings.NewReader(abiJSON))
		if err != nil {
			return chain.Address{}, nil, fmt.Errorf("simchain: parse abi: %w", err)
		}
		packed, err := parsed.Pack("", args...)
		if err != nil {
			return chain.Address{}, nil, fmt.Errorf("simchain: pack constructor: %w", err)
		}
		data = append(data, packed...)
	}
	gas, err := c.client.EstimateGas(ctx, ethereum.CallMsg{From: from.Address, Data: data})
	if err != nil {
		return chain.Address{}, nil, fmt.Errorf("simchain: estimate deploy gas: %w", err)
	}
	tx, err := c.NewDynamicFeeTx(ctx, from, nil, nil, gas, data)
	if err != nil {
		return chain.Address{}, nil, err
	}
	signed, err := c.SignTx(from, tx)
	if err != nil {
		return chain.Address{}, nil, err
	}
	if err := c.client.SendTransaction(ctx, signed); err != nil {
		return chain.Address{}, nil, fmt.Errorf("simchain: send deploy: %w", err)
	}
	c.Commit()
	receipt, err := c.client.TransactionReceipt(ctx, signed.Hash())
	if err != nil {
		return chain.Address{}, nil, fmt.Errorf("simchain: deploy receipt: %w", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return chain.Address{}, receipt, errors.New("simchain: deploy reverted")
	}
	return receipt.ContractAddress, receipt, nil
}

// RPC adapts simulated.Client to rpc.RPC.
type RPC struct {
	c simulated.Client
}

var _ rpc.RPC = (*RPC)(nil)

func (r *RPC) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	return r.c.CallContract(ctx, msg, blockNumber)
}

func (r *RPC) PendingCallContract(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
	return r.c.PendingCallContract(ctx, msg)
}

func (r *RPC) CodeAt(ctx context.Context, addr chain.Address, blockNumber *big.Int) ([]byte, error) {
	return r.c.CodeAt(ctx, addr, blockNumber)
}

func (r *RPC) EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
	return r.c.EstimateGas(ctx, msg)
}

func (r *RPC) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	return r.c.SendTransaction(ctx, tx)
}

func (r *RPC) TransactionByHash(ctx context.Context, hash chain.TxHash) (*types.Transaction, bool, error) {
	tx, pending, err := r.c.TransactionByHash(ctx, hash)
	return tx, pending, normalizeLookupErr(err)
}

func (r *RPC) TransactionReceipt(ctx context.Context, hash chain.TxHash) (*types.Receipt, error) {
	receipt, err := r.c.TransactionReceipt(ctx, hash)
	return receipt, normalizeLookupErr(err)
}

// normalizeLookupErr maps the fresh node's "transaction indexing is in
// progress" answer to ethereum.NotFound. A production endpoint has long
// finished indexing and answers NotFound for an unknown or unmined hash;
// consumers (the reorg receipts provider among them) poll on NotFound and
// treat anything else as a failure, so the simulated node must speak the
// same way.
func normalizeLookupErr(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "transaction indexing is in progress") {
		return ethereum.NotFound
	}
	return err
}

func (r *RPC) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	return r.c.BlockByNumber(ctx, number)
}

func (r *RPC) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	return r.c.HeaderByNumber(ctx, number)
}

func (r *RPC) FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	return r.c.FilterLogs(ctx, query)
}

func (r *RPC) PendingNonceAt(ctx context.Context, addr chain.Address) (uint64, error) {
	return r.c.PendingNonceAt(ctx, addr)
}

func (r *RPC) BalanceAt(ctx context.Context, addr chain.Address, blockNumber *big.Int) (*big.Int, error) {
	return r.c.BalanceAt(ctx, addr, blockNumber)
}

func (r *RPC) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return r.c.SuggestGasPrice(ctx)
}

func (r *RPC) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	return r.c.SuggestGasTipCap(ctx)
}

func (r *RPC) ChainID(ctx context.Context) (chain.ChainID, error) {
	id, err := r.c.ChainID(ctx)
	if err != nil {
		return 0, err
	}
	return chain.ChainID(id.Uint64()), nil
}

// Close is a no-op: the Chain owns the backend.
func (r *RPC) Close() error { return nil }

// BlockNumber is not part of rpc.RPC but is handy in tests.
func (r *RPC) BlockNumber(ctx context.Context) (uint64, error) {
	return r.c.BlockNumber(ctx)
}
