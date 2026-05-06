// Package ticketbroker is the chain-backed implementation of
// providers.Broker against the on-chain TicketBroker contract on
// Arbitrum One.
//
// Read methods (GetSenderInfo, IsUsedTicket) issue eth_call against the
// resolved contract address. RedeemWinningTicket signs the tx with the
// supplied TxSigner, broadcasts via eth_sendRawTransaction, polls
// eth_getTransactionReceipt until the receipt + Config.Confirmations
// blocks have passed, and returns the confirmed tx hash.
//
// Per plan 0016 §11.Q1 we deliberately do NOT port the prior impl's
// chain-commons.txintent layer — settlement here is single-threaded,
// one tx per loop tick, and go-ethereum's bind.TransactOpts surface +
// an in-process nonce counter is sufficient.
package ticketbroker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers"
)

// Sentinel errors returned by Redeem-side flow. Settlement classifies
// these into retryable / non-retryable buckets.
var (
	// ErrTxFailed indicates the redemption tx mined but the receipt
	// reports status=0 (revert). Non-retryable.
	ErrTxFailed = errors.New("ticketbroker: transaction failed")

	// ErrReorged indicates the tx receipt disappeared mid-confirmation
	// wait. Retryable.
	ErrReorged = errors.New("ticketbroker: redemption tx reorged out")
)

// Config holds the parameters for a Broker instance.
type Config struct {
	// Address is the deployed TicketBroker contract address.
	Address ethcommon.Address

	// Claimant is the receiver-side ETH address used as the second
	// argument to claimedReserve(reserveHolder, claimant). For receiver
	// mode this is the orchestrator address (or, when no separate
	// orch identity is configured, the keystore signer).
	Claimant ethcommon.Address

	// From is the EOA submitting redeemWinningTicket transactions. Used
	// for nonce assignment and log stamping. Should match the TxSigner's
	// address; mismatch is a config bug.
	From ethcommon.Address

	// ChainID is the connected chain's ID, used by the TxSigner and to
	// guard against wrong-chain submission.
	ChainID *big.Int

	// RedeemGas is the gas limit used for redeemWinningTicket. Zero =
	// 500_000 (Arbitrum L2 empirical cost).
	RedeemGas uint64

	// Confirmations is how many blocks past the receipt we wait before
	// declaring the tx confirmed. Default 4 (per runbook §3 / plan §3.3).
	Confirmations uint64

	// Logger receives structured events. Nil = slog.Default().
	Logger *slog.Logger
}

const defaultRedeemGas uint64 = 500_000
const defaultConfirmations uint64 = 4

// Broker is the chain-backed providers.Broker.
type Broker struct {
	cfg      Config
	client   *ethclient.Client
	gasPrice providers.GasPrice
	signer   providers.TxSigner
	log      *slog.Logger

	mu    sync.Mutex
	nonce uint64
	noncePrimed bool
}

// New constructs a Broker. client + signer + gasPrice are all required
// for the redeem path; sender mode (which never redeems) may pass nil
// signer/gasPrice.
func New(cfg Config, client *ethclient.Client, gasPrice providers.GasPrice, signer providers.TxSigner) (*Broker, error) {
	if client == nil {
		return nil, errors.New("ticketbroker: nil ethclient")
	}
	if (cfg.Address == ethcommon.Address{}) {
		return nil, errors.New("ticketbroker: empty contract address")
	}
	if cfg.RedeemGas == 0 {
		cfg.RedeemGas = defaultRedeemGas
	}
	if cfg.Confirmations == 0 {
		cfg.Confirmations = defaultConfirmations
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Broker{
		cfg:      cfg,
		client:   client,
		gasPrice: gasPrice,
		signer:   signer,
		log:      logger.With("component", "ticketbroker"),
	}, nil
}

// GetSenderInfo implements providers.Broker.
func (b *Broker) GetSenderInfo(ctx context.Context, sender []byte) (*providers.SenderInfo, error) {
	if len(sender) != 20 {
		return nil, fmt.Errorf("ticketbroker: sender must be 20 bytes, got %d", len(sender))
	}
	addr := ethcommon.BytesToAddress(sender)

	data, err := ParsedABI.Pack("getSenderInfo", addr)
	if err != nil {
		return nil, fmt.Errorf("pack getSenderInfo: %w", err)
	}
	out, err := b.client.CallContract(ctx, ethereum.CallMsg{To: &b.cfg.Address, Data: data}, nil)
	if err != nil {
		return nil, fmt.Errorf("call getSenderInfo: %w", err)
	}
	var decoded senderInfoResult
	if err := ParsedABI.UnpackIntoInterface(&decoded, "getSenderInfo", out); err != nil {
		return nil, fmt.Errorf("unpack getSenderInfo: %w", err)
	}

	withdrawRound := int64(0)
	if decoded.Sender.WithdrawRound != nil && decoded.Sender.WithdrawRound.IsInt64() {
		withdrawRound = decoded.Sender.WithdrawRound.Int64()
	}

	claimedByMe, err := b.claimedReserve(ctx, addr, b.cfg.Claimant)
	if err != nil {
		return nil, fmt.Errorf("call claimedReserve: %w", err)
	}

	reserve := &providers.Reserve{
		FundsRemaining: nilToZero(decoded.Reserve.FundsRemaining),
		Claimed:        map[string]*big.Int{},
	}
	if (b.cfg.Claimant != ethcommon.Address{}) {
		reserve.Claimed[strings.ToLower(b.cfg.Claimant.Hex())] = claimedByMe
	}
	return &providers.SenderInfo{
		Deposit:       nilToZero(decoded.Sender.Deposit),
		Reserve:       reserve,
		WithdrawRound: withdrawRound,
	}, nil
}

// IsUsedTicket implements providers.Broker.
func (b *Broker) IsUsedTicket(ctx context.Context, ticketHash []byte) (bool, error) {
	if len(ticketHash) != 32 {
		return false, fmt.Errorf("ticketbroker: ticketHash must be 32 bytes, got %d", len(ticketHash))
	}
	var hash [32]byte
	copy(hash[:], ticketHash)
	data, err := ParsedABI.Pack("usedTickets", hash)
	if err != nil {
		return false, fmt.Errorf("pack usedTickets: %w", err)
	}
	out, err := b.client.CallContract(ctx, ethereum.CallMsg{To: &b.cfg.Address, Data: data}, nil)
	if err != nil {
		return false, fmt.Errorf("call usedTickets: %w", err)
	}
	decoded, err := ParsedABI.Unpack("usedTickets", out)
	if err != nil {
		return false, fmt.Errorf("unpack usedTickets: %w", err)
	}
	if len(decoded) != 1 {
		return false, fmt.Errorf("usedTickets: expected 1 return value, got %d", len(decoded))
	}
	used, ok := decoded[0].(bool)
	if !ok {
		return false, fmt.Errorf("usedTickets: unexpected return type %T", decoded[0])
	}
	return used, nil
}

// RedeemWinningTicket implements providers.Broker.
func (b *Broker) RedeemWinningTicket(ctx context.Context, t *providers.Ticket, sig []byte, recipientRand *big.Int) ([]byte, error) {
	if t == nil {
		return nil, errors.New("ticketbroker: nil ticket")
	}
	if recipientRand == nil {
		return nil, errors.New("ticketbroker: nil recipientRand")
	}
	if b.signer == nil {
		return nil, errors.New("ticketbroker: nil TxSigner; broker is read-only")
	}
	if b.gasPrice == nil {
		return nil, errors.New("ticketbroker: nil GasPrice; broker is read-only")
	}
	if b.cfg.ChainID == nil {
		return nil, errors.New("ticketbroker: nil ChainID")
	}

	data, err := ParsedABI.Pack("redeemWinningTicket", toSolTicket(t), sig, recipientRand)
	if err != nil {
		return nil, fmt.Errorf("pack redeemWinningTicket: %w", err)
	}

	nonce, err := b.nextNonce(ctx)
	if err != nil {
		return nil, fmt.Errorf("get nonce: %w", err)
	}
	gp := b.gasPrice.Current()
	if gp == nil || gp.Sign() == 0 {
		return nil, errors.New("ticketbroker: gas price unavailable")
	}

	tx := ethtypes.NewTx(&ethtypes.LegacyTx{
		Nonce:    nonce,
		To:       &b.cfg.Address,
		Value:    new(big.Int),
		Gas:      b.cfg.RedeemGas,
		GasPrice: new(big.Int).Set(gp),
		Data:     data,
	})
	signed, err := b.signer.SignTx(tx, b.cfg.ChainID)
	if err != nil {
		return nil, fmt.Errorf("sign tx: %w", err)
	}
	if err := b.client.SendTransaction(ctx, signed); err != nil {
		// Reset nonce primer so the next attempt re-queries the
		// pending-nonce in case our local counter drifted past chain.
		b.mu.Lock()
		b.noncePrimed = false
		b.mu.Unlock()
		return nil, fmt.Errorf("send tx: %w", err)
	}

	txHash := signed.Hash()
	b.log.Info("redemption tx submitted",
		"tx_hash", txHash.Hex(),
		"from", b.cfg.From.Hex(),
		"gas_limit", b.cfg.RedeemGas,
		"gas_price_wei", gp.String(),
		"nonce", nonce,
	)

	receipt, err := b.waitForReceipt(ctx, txHash)
	if err != nil {
		return nil, err
	}
	if receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("%w: receipt status=%d, block=%s", ErrTxFailed, receipt.Status, receipt.BlockNumber.String())
	}
	if err := b.waitForConfirmations(ctx, receipt.BlockNumber); err != nil {
		return nil, err
	}
	b.log.Info("redemption confirmed",
		"tx_hash", txHash.Hex(),
		"block", receipt.BlockNumber.String(),
		"gas_used", receipt.GasUsed,
	)
	return txHash.Bytes(), nil
}

// claimedReserve calls TicketBroker.claimedReserve(reserveHolder, claimant).
func (b *Broker) claimedReserve(ctx context.Context, reserveHolder, claimant ethcommon.Address) (*big.Int, error) {
	if (claimant == ethcommon.Address{}) {
		return new(big.Int), nil
	}
	data, err := ParsedABI.Pack("claimedReserve", reserveHolder, claimant)
	if err != nil {
		return nil, fmt.Errorf("pack claimedReserve: %w", err)
	}
	out, err := b.client.CallContract(ctx, ethereum.CallMsg{To: &b.cfg.Address, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	decoded, err := ParsedABI.Unpack("claimedReserve", out)
	if err != nil {
		return nil, fmt.Errorf("unpack claimedReserve: %w", err)
	}
	if len(decoded) != 1 {
		return nil, fmt.Errorf("claimedReserve: expected 1 return value, got %d", len(decoded))
	}
	v, ok := decoded[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("claimedReserve: unexpected return type %T", decoded[0])
	}
	return v, nil
}

func (b *Broker) nextNonce(ctx context.Context) (uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.noncePrimed {
		n, err := b.client.PendingNonceAt(ctx, b.cfg.From)
		if err != nil {
			return 0, err
		}
		b.nonce = n
		b.noncePrimed = true
	}
	out := b.nonce
	b.nonce++
	return out, nil
}

func (b *Broker) waitForReceipt(ctx context.Context, txHash ethcommon.Hash) (*ethtypes.Receipt, error) {
	const pollInterval = 2 * time.Second
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		receipt, err := b.client.TransactionReceipt(ctx, txHash)
		if err == nil && receipt != nil {
			return receipt, nil
		}
		if err != nil && !errors.Is(err, ethereum.NotFound) {
			return nil, fmt.Errorf("get receipt: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func (b *Broker) waitForConfirmations(ctx context.Context, mined *big.Int) error {
	const pollInterval = 2 * time.Second
	target := new(big.Int).Add(mined, big.NewInt(int64(b.cfg.Confirmations)))
	for {
		head, err := b.client.BlockNumber(ctx)
		if err != nil {
			return fmt.Errorf("block number: %w", err)
		}
		if new(big.Int).SetUint64(head).Cmp(target) >= 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func toSolTicket(t *providers.Ticket) solTicket {
	out := solTicket{
		Recipient:   ethcommon.BytesToAddress(t.Recipient),
		Sender:      ethcommon.BytesToAddress(t.Sender),
		FaceValue:   nilToZero(t.FaceValue),
		WinProb:     nilToZero(t.WinProb),
		SenderNonce: new(big.Int).SetUint64(uint64(t.SenderNonce)),
		AuxData:     auxData(t),
	}
	copy(out.RecipientRandHash[:], leftPad32(t.RecipientRandHash))
	return out
}

func auxData(t *providers.Ticket) []byte {
	if t.CreationRound == 0 && allZero(t.CreationRoundHash) {
		return []byte{}
	}
	out := make([]byte, 0, 64)
	out = append(out, ethcommon.LeftPadBytes(big.NewInt(t.CreationRound).Bytes(), 32)...)
	out = append(out, leftPad32(t.CreationRoundHash)...)
	return out
}

func leftPad32(b []byte) []byte {
	if len(b) >= 32 {
		out := make([]byte, 32)
		copy(out, b[len(b)-32:])
		return out
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

func nilToZero(v *big.Int) *big.Int {
	if v == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(v)
}
