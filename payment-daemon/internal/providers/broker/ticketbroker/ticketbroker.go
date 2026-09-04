// Package ticketbroker is the chain-backed implementation of
// providers.Broker against the on-chain TicketBroker contract on
// Arbitrum One.
//
// Read methods (GetSenderInfo, IsUsedTicket, TicketValidityPeriod) issue
// eth_call against the resolved contract address through chain-commons
// rpc.RPC, so they fail over across --chain-rpc-urls.
//
// RedeemWinningTicket does not sign or send anything itself. It
// pre-checks usedTickets, then hands the redemption to chain-commons's
// durable transaction state machine (services/txintent) as an intent
// keyed by the ticket hash, and waits for the terminal state. The
// intent processor owns the wallet's nonce, the gas caps, replacement
// on stall, reorg recovery and confirmation tracking; the broker maps
// the outcome back onto the providers.Broker contract (plan 0048 stage
// 4b).
package ticketbroker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum"
	ethcommon "github.com/ethereum/go-ethereum/common"

	cerrors "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/errors"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/rpc"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/types"
)

// ErrTxFailed is returned when the redemption transaction mined but
// reverted. It is the providers-level sentinel; settlement classifies
// it as terminal and drains the ticket.
var ErrTxFailed = providers.ErrRedemptionReverted

// IntentKind is the txintent Kind under which redemptions are filed.
// Together with the ticket hash it is the idempotency key: the same
// ticket submitted twice, in one process or across a restart, yields
// one intent and at most one transaction.
const IntentKind = "RedeemTicket"

// Intents is the slice of *txintent.Manager the broker depends on.
type Intents interface {
	Submit(ctx context.Context, p txintent.Params) (txintent.IntentID, error)
	Status(ctx context.Context, id txintent.IntentID) (txintent.TxIntent, error)
	Resubmit(ctx context.Context, id txintent.IntentID, calldata []byte) error
	Wait(ctx context.Context, id txintent.IntentID) (txintent.TxIntent, error)
}

// Config holds the parameters for a Broker instance.
type Config struct {
	// Address is the deployed TicketBroker contract address.
	Address ethcommon.Address

	// Claimant is the receiver-side ETH address used as the second
	// argument to claimedReserve(reserveHolder, claimant). For receiver
	// mode this is the orchestrator address (or, when no separate
	// orch identity is configured, the keystore signer).
	Claimant ethcommon.Address

	// RedeemGas is the gas limit used for redeemWinningTicket. Zero =
	// 500_000 (Arbitrum L2 empirical cost).
	RedeemGas uint64

	// Logger receives structured events. Nil = slog.Default().
	Logger *slog.Logger
}

const defaultRedeemGas uint64 = 500_000

// Broker is the chain-backed providers.Broker.
type Broker struct {
	cfg     Config
	client  rpc.RPC
	intents Intents
	log     *slog.Logger

	// submitMu serializes the submit / inspect / resubmit step. The
	// intent manager's idempotency is keyed on the ticket hash, but two
	// concurrent first submits of the same key can both miss the read
	// and both dispatch a processor; one redemption per ticket is this
	// broker's promise, so the critical section is held here. Waiting
	// happens outside the lock.
	submitMu sync.Mutex
}

// New constructs a Broker. client is required. intents is required for
// the redeem path; sender mode (which never redeems) passes nil and gets
// a read-only broker.
func New(cfg Config, client rpc.RPC, intents Intents) (*Broker, error) {
	if client == nil {
		return nil, errors.New("ticketbroker: nil rpc client")
	}
	if (cfg.Address == ethcommon.Address{}) {
		return nil, errors.New("ticketbroker: empty contract address")
	}
	if cfg.RedeemGas == 0 {
		cfg.RedeemGas = defaultRedeemGas
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Broker{
		cfg:     cfg,
		client:  client,
		intents: intents,
		log:     logger.With("component", "ticketbroker"),
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

// TicketHash returns the contract-defined hash of t: the value the
// TicketBroker records in usedTickets and the idempotency key of the
// redemption intent.
func TicketHash(t *providers.Ticket) []byte {
	tt := types.Ticket{
		Recipient:         t.Recipient,
		Sender:            t.Sender,
		FaceValue:         t.FaceValue,
		WinProb:           t.WinProb,
		SenderNonce:       t.SenderNonce,
		RecipientRandHash: t.RecipientRandHash,
		CreationRound:     t.CreationRound,
		CreationRoundHash: t.CreationRoundHash,
	}
	return tt.Hash()
}

// RedeemWinningTicket implements providers.Broker.
//
// Reconciliation across an upgrade or restart (plan 0048 §2.5) rests on
// two things this method does in order:
//
//  1. usedTickets is read first. A redemption the pre-upgrade loop (or
//     an earlier attempt of this one) already landed returns
//     ErrTicketAlreadyUsed without any transaction.
//  2. The intent's idempotency key is (IntentKind, ticket hash). A
//     ticket already in the intent store — in flight, or confirmed
//     during downtime and picked up by Resume — is never re-sent; the
//     broker just waits on the existing intent.
func (b *Broker) RedeemWinningTicket(ctx context.Context, t *providers.Ticket, sig []byte, recipientRand *big.Int) ([]byte, error) {
	if t == nil {
		return nil, errors.New("ticketbroker: nil ticket")
	}
	if recipientRand == nil {
		return nil, errors.New("ticketbroker: nil recipientRand")
	}
	if b.intents == nil {
		return nil, errors.New("ticketbroker: nil intent manager; broker is read-only")
	}

	hash := TicketHash(t)
	logCtx := b.log.With("ticket_hash", ethcommon.Bytes2Hex(hash))

	used, err := b.IsUsedTicket(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("pre-check usedTickets: %w", err)
	}
	if used {
		logCtx.Info("ticket already redeemed on-chain; no transaction")
		return nil, providers.ErrTicketAlreadyUsed
	}

	data, err := ParsedABI.Pack("redeemWinningTicket", toSolTicket(t), sig, recipientRand)
	if err != nil {
		return nil, fmt.Errorf("pack redeemWinningTicket: %w", err)
	}

	id, err := b.submitOrRedrive(ctx, hash, data, t.Sender, logCtx)
	if err != nil {
		return nil, err
	}

	final, err := b.intents.Wait(ctx, id)
	if err != nil {
		// Typically the settlement tick's deadline. The intent keeps
		// going in the background; the next tick submits the same key
		// and waits on it again.
		return nil, fmt.Errorf("wait for redemption intent %s: %w", id.Hex(), err)
	}
	switch final.Status {
	case txintent.StatusConfirmed:
		attempt := final.CurrentAttempt()
		if attempt == nil {
			return nil, fmt.Errorf("ticketbroker: intent %s confirmed without an attempt", id.Hex())
		}
		logCtx.Info("redemption confirmed",
			"tx_hash", attempt.SignedTxHash.Hex(),
			"nonce", attempt.Nonce,
			"attempts", len(final.Attempts),
		)
		return attempt.SignedTxHash.Bytes(), nil
	case txintent.StatusFailed:
		if isRevert(final.FailedReason) {
			return nil, revertError(final)
		}
		return nil, fmt.Errorf("redemption intent %s failed: %w", id.Hex(), reasonError(final.FailedReason))
	default:
		return nil, fmt.Errorf("ticketbroker: intent %s left Wait in non-terminal state %s", id.Hex(), final.Status)
	}
}

// submitOrRedrive files the redemption intent and, when an earlier
// attempt at this ticket ended in a non-revert failure, re-drives it.
func (b *Broker) submitOrRedrive(ctx context.Context, hash, data []byte, sender []byte, logCtx *slog.Logger) (txintent.IntentID, error) {
	b.submitMu.Lock()
	defer b.submitMu.Unlock()

	id, err := b.intents.Submit(ctx, txintent.Params{
		Kind:      IntentKind,
		KeyParams: hash,
		To:        b.cfg.Address,
		CallData:  data,
		GasLimit:  b.cfg.RedeemGas,
		Metadata: map[string]string{
			"ticket_hash": ethcommon.Bytes2Hex(hash),
			"sender":      ethcommon.BytesToAddress(sender).Hex(),
		},
	})
	if err != nil {
		return txintent.IntentID{}, fmt.Errorf("submit redemption intent: %w", err)
	}

	// A prior attempt at this ticket may have ended in a terminal
	// failure. A revert is final for the ticket. Anything else — a
	// replacement that timed out, a transport failure the processor gave
	// up on, a wallet that was out of gas — is worth another
	// transaction, and Resubmit is the sanctioned way past the
	// idempotency guard for a failed intent.
	cur, err := b.intents.Status(ctx, id)
	if err != nil {
		return txintent.IntentID{}, fmt.Errorf("intent status: %w", err)
	}
	if cur.Status == txintent.StatusFailed {
		if isRevert(cur.FailedReason) {
			return txintent.IntentID{}, revertError(cur)
		}
		logCtx.Info("re-driving failed redemption intent",
			"intent", id.Hex(),
			"reason", reasonString(cur.FailedReason),
		)
		if err := b.intents.Resubmit(ctx, id, data); err != nil {
			return txintent.IntentID{}, fmt.Errorf("resubmit redemption intent: %w", err)
		}
	}
	return id, nil
}

func isRevert(reason *cerrors.Error) bool {
	return reason != nil && reason.Class == cerrors.ClassReverted
}

// revertError wraps both the providers sentinel and the classified
// cause, so errors.Is(err, ErrTxFailed) and cerrors.Classify(err) agree.
func revertError(t txintent.TxIntent) error {
	tx := "?"
	if a := t.CurrentAttempt(); a != nil {
		tx = a.SignedTxHash.Hex()
	}
	return fmt.Errorf("%w: tx %s: %w", ErrTxFailed, tx, reasonError(t.FailedReason))
}

func reasonError(reason *cerrors.Error) error {
	if reason == nil {
		return cerrors.New(cerrors.ClassUnknown, "txintent.failed_without_reason", "intent failed without a recorded reason")
	}
	return reason
}

func reasonString(reason *cerrors.Error) string {
	if reason == nil {
		return "<none>"
	}
	return reason.Error()
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

// TicketValidityPeriod implements providers.Broker by reading the
// contract parameter rather than assuming it.
//
// go-livepeer keeps a hardcoded mirror (pm/queue.go's
// ticketValidityPeriod = 2) and so did we. That is fine for deciding
// which of your own queued tickets are still worth gas, and not fine for
// telling a third party how long a payment envelope stays spendable:
// governance can raise it (setTicketValidityPeriod), and an understated
// value makes a consumer release an encumbrance while the envelope can
// still be redeemed.
func (b *Broker) TicketValidityPeriod(ctx context.Context) (int64, error) {
	data, err := ParsedABI.Pack("ticketValidityPeriod")
	if err != nil {
		return 0, fmt.Errorf("pack ticketValidityPeriod: %w", err)
	}
	out, err := b.client.CallContract(ctx, ethereum.CallMsg{To: &b.cfg.Address, Data: data}, nil)
	if err != nil {
		return 0, fmt.Errorf("call ticketValidityPeriod: %w", err)
	}
	decoded, err := ParsedABI.Unpack("ticketValidityPeriod", out)
	if err != nil {
		return 0, fmt.Errorf("unpack ticketValidityPeriod: %w", err)
	}
	if len(decoded) != 1 {
		return 0, fmt.Errorf("ticketValidityPeriod: expected 1 return value, got %d", len(decoded))
	}
	v, ok := decoded[0].(*big.Int)
	if !ok {
		return 0, fmt.Errorf("ticketValidityPeriod: unexpected return type %T", decoded[0])
	}
	if !v.IsInt64() || v.Int64() < 1 {
		return 0, fmt.Errorf("ticketValidityPeriod: implausible value %s", v)
	}
	return v.Int64(), nil
}
