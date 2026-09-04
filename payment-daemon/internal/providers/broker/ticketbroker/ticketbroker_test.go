package ticketbroker

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	cchain "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	chaintesting "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/keystore/inmemory"
)

var (
	contractAddr = ethcommon.HexToAddress("0x1111111111111111111111111111111111111111")
	claimantAddr = ethcommon.HexToAddress("0x2222222222222222222222222222222222222222")
	senderAddr   = ethcommon.HexToAddress("0x3333333333333333333333333333333333333333")
)

// abiOut packs a method's return values as the contract would.
func abiOut(t *testing.T, method string, args ...any) []byte {
	t.Helper()
	out, err := ParsedABI.Methods[method].Outputs.Pack(args...)
	if err != nil {
		t.Fatalf("pack %s: %v", method, err)
	}
	return out
}

// contractStub answers eth_call by selector against the TicketBroker
// address; anything else is an error so a wrong `to` is caught.
func contractStub(t *testing.T, answers map[string][]byte) *chaintesting.FakeRPC {
	t.Helper()
	f := chaintesting.NewFakeRPC()
	f.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		if msg.To == nil || *msg.To != contractAddr {
			return nil, errors.New("eth_call sent to the wrong contract")
		}
		if len(msg.Data) < 4 {
			return nil, errors.New("short calldata")
		}
		for name, out := range answers {
			if bytes.Equal(msg.Data[:4], ParsedABI.Methods[name].ID) {
				return out, nil
			}
		}
		return nil, errors.New("unstubbed selector")
	}
	return f
}

type fixedGas struct{ v *big.Int }

func (g fixedGas) Current() *big.Int { return g.v }

func TestGetSenderInfo_DecodesTuples(t *testing.T) {
	deposit := big.NewInt(1_000_000_000_000_000_000)
	fundsRemaining := big.NewInt(500_000_000_000_000_000)
	f := contractStub(t, map[string][]byte{
		"getSenderInfo": abiOut(t, "getSenderInfo",
			struct {
				Deposit       *big.Int `abi:"deposit"`
				WithdrawRound *big.Int `abi:"withdrawRound"`
			}{Deposit: deposit, WithdrawRound: big.NewInt(0)},
			struct {
				FundsRemaining        *big.Int `abi:"fundsRemaining"`
				ClaimedInCurrentRound *big.Int `abi:"claimedInCurrentRound"`
			}{FundsRemaining: fundsRemaining, ClaimedInCurrentRound: big.NewInt(0)},
		),
		"claimedReserve": abiOut(t, "claimedReserve", big.NewInt(42)),
	})
	b, err := New(Config{Address: contractAddr, Claimant: claimantAddr, ChainID: big.NewInt(42161)}, f, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	info, err := b.GetSenderInfo(context.Background(), senderAddr.Bytes())
	if err != nil {
		t.Fatalf("GetSenderInfo: %v", err)
	}
	if info.Deposit.Cmp(deposit) != 0 {
		t.Errorf("deposit = %s; want %s", info.Deposit, deposit)
	}
	if info.WithdrawRound != 0 {
		t.Errorf("withdrawRound = %d; want 0", info.WithdrawRound)
	}
	if info.Reserve == nil || info.Reserve.FundsRemaining.Cmp(fundsRemaining) != 0 {
		t.Errorf("FundsRemaining = %v; want %s", info.Reserve, fundsRemaining)
	}
	if got := info.Reserve.Claimed["0x2222222222222222222222222222222222222222"]; got == nil || got.Cmp(big.NewInt(42)) != 0 {
		t.Errorf("Claimed[my-claimant] = %v; want 42", got)
	}
	if f.CallCount("CallContract") != 2 {
		t.Errorf("eth_call count = %d; want 2 (getSenderInfo + claimedReserve)", f.CallCount("CallContract"))
	}
}

func TestGetSenderInfo_NoClaimantSkipsClaimedReserve(t *testing.T) {
	f := contractStub(t, map[string][]byte{
		"getSenderInfo": abiOut(t, "getSenderInfo",
			struct {
				Deposit       *big.Int `abi:"deposit"`
				WithdrawRound *big.Int `abi:"withdrawRound"`
			}{Deposit: big.NewInt(5), WithdrawRound: big.NewInt(9)},
			struct {
				FundsRemaining        *big.Int `abi:"fundsRemaining"`
				ClaimedInCurrentRound *big.Int `abi:"claimedInCurrentRound"`
			}{FundsRemaining: big.NewInt(6), ClaimedInCurrentRound: big.NewInt(0)},
		),
	})
	b, _ := New(Config{Address: contractAddr, ChainID: big.NewInt(42161)}, f, nil, nil)
	info, err := b.GetSenderInfo(context.Background(), senderAddr.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if info.WithdrawRound != 9 || len(info.Reserve.Claimed) != 0 {
		t.Fatalf("info = %+v", info)
	}
	if f.CallCount("CallContract") != 1 {
		t.Errorf("eth_call count = %d; want 1", f.CallCount("CallContract"))
	}
}

func TestGetSenderInfo_Errors(t *testing.T) {
	f := contractStub(t, map[string][]byte{"getSenderInfo": []byte{0x01}})
	b, _ := New(Config{Address: contractAddr, ChainID: big.NewInt(42161)}, f, nil, nil)
	if _, err := b.GetSenderInfo(context.Background(), []byte{1, 2}); err == nil || !strings.Contains(err.Error(), "20 bytes") {
		t.Errorf("short sender: %v", err)
	}
	if _, err := b.GetSenderInfo(context.Background(), senderAddr.Bytes()); err == nil || !strings.Contains(err.Error(), "unpack getSenderInfo") {
		t.Errorf("garbage return: %v", err)
	}
	f.InjectError("CallContract", errors.New("connection refused"))
	if _, err := b.GetSenderInfo(context.Background(), senderAddr.Bytes()); err == nil || !strings.Contains(err.Error(), "call getSenderInfo") {
		t.Errorf("rpc error: %v", err)
	}
}

func TestIsUsedTicket_DecodesBool(t *testing.T) {
	f := contractStub(t, map[string][]byte{"usedTickets": abiOut(t, "usedTickets", true)})
	b, _ := New(Config{Address: contractAddr, ChainID: big.NewInt(42161)}, f, nil, nil)
	ticketHash := make([]byte, 32)
	ticketHash[0] = 0xde
	used, err := b.IsUsedTicket(context.Background(), ticketHash)
	if err != nil {
		t.Fatalf("IsUsedTicket: %v", err)
	}
	if !used {
		t.Error("expected used=true")
	}
	if _, err := b.IsUsedTicket(context.Background(), ticketHash[:5]); err == nil {
		t.Error("short hash must error")
	}
	f.InjectError("CallContract", errors.New("down"))
	if _, err := b.IsUsedTicket(context.Background(), ticketHash); err == nil || !strings.Contains(err.Error(), "call usedTickets") {
		t.Errorf("rpc error: %v", err)
	}
}

func TestTicketValidityPeriod(t *testing.T) {
	f := contractStub(t, map[string][]byte{"ticketValidityPeriod": abiOut(t, "ticketValidityPeriod", big.NewInt(2))})
	b, _ := New(Config{Address: contractAddr, ChainID: big.NewInt(42161)}, f, nil, nil)
	got, err := b.TicketValidityPeriod(context.Background())
	if err != nil || got != 2 {
		t.Fatalf("got %d, %v; want 2", got, err)
	}
	f = contractStub(t, map[string][]byte{"ticketValidityPeriod": abiOut(t, "ticketValidityPeriod", big.NewInt(0))})
	b, _ = New(Config{Address: contractAddr, ChainID: big.NewInt(42161)}, f, nil, nil)
	if _, err := b.TicketValidityPeriod(context.Background()); err == nil || !strings.Contains(err.Error(), "implausible") {
		t.Errorf("zero period: %v", err)
	}
	f.InjectError("CallContract", errors.New("down"))
	if _, err := b.TicketValidityPeriod(context.Background()); err == nil {
		t.Error("rpc error must surface")
	}
}

func TestNew_Validation(t *testing.T) {
	f := chaintesting.NewFakeRPC()
	if _, err := New(Config{Address: contractAddr}, nil, nil, nil); err == nil {
		t.Error("nil client must error")
	}
	if _, err := New(Config{}, f, nil, nil); err == nil {
		t.Error("empty address must error")
	}
	b, err := New(Config{Address: contractAddr}, f, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.cfg.RedeemGas != defaultRedeemGas || b.cfg.Confirmations != defaultConfirmations {
		t.Errorf("defaults not applied: %+v", b.cfg)
	}
}

func sampleTicket() *providers.Ticket {
	return &providers.Ticket{
		Recipient:         claimantAddr.Bytes(),
		Sender:            senderAddr.Bytes(),
		FaceValue:         big.NewInt(1),
		WinProb:           big.NewInt(1),
		SenderNonce:       1,
		RecipientRandHash: make([]byte, 32),
	}
}

func TestRedeem_RejectsMisconfiguration(t *testing.T) {
	f := chaintesting.NewFakeRPC()
	ctx := context.Background()
	b, _ := New(Config{Address: contractAddr, ChainID: big.NewInt(42161)}, f, nil, nil)
	if _, err := b.RedeemWinningTicket(ctx, sampleTicket(), make([]byte, 65), big.NewInt(1)); err == nil || !strings.Contains(err.Error(), "TxSigner") {
		t.Errorf("read-only broker: %v", err)
	}
	if _, err := b.RedeemWinningTicket(ctx, nil, nil, big.NewInt(1)); err == nil {
		t.Error("nil ticket must error")
	}
	if _, err := b.RedeemWinningTicket(ctx, sampleTicket(), nil, nil); err == nil {
		t.Error("nil recipientRand must error")
	}
	key, _ := crypto.GenerateKey()
	signer, _ := inmemory.New(key)
	b, _ = New(Config{Address: contractAddr, ChainID: big.NewInt(42161)}, f, nil, signer)
	if _, err := b.RedeemWinningTicket(ctx, sampleTicket(), make([]byte, 65), big.NewInt(1)); err == nil || !strings.Contains(err.Error(), "GasPrice") {
		t.Errorf("nil gas price: %v", err)
	}
	b, _ = New(Config{Address: contractAddr}, f, fixedGas{big.NewInt(10)}, signer)
	if _, err := b.RedeemWinningTicket(ctx, sampleTicket(), make([]byte, 65), big.NewInt(1)); err == nil || !strings.Contains(err.Error(), "ChainID") {
		t.Errorf("nil chain id: %v", err)
	}
	b, _ = New(Config{Address: contractAddr, ChainID: big.NewInt(42161)}, f, fixedGas{big.NewInt(0)}, signer)
	if _, err := b.RedeemWinningTicket(ctx, sampleTicket(), make([]byte, 65), big.NewInt(1)); err == nil || !strings.Contains(err.Error(), "gas price unavailable") {
		t.Errorf("zero gas price: %v", err)
	}
}

// redeemHarness wires a signer, a fixed gas price and a FakeRPC whose
// SendTransaction / TransactionReceipt / HeaderByNumber are scripted.
type redeemHarness struct {
	rpc    *chaintesting.FakeRPC
	broker *Broker
	from   ethcommon.Address
	sent   atomic.Pointer[ethtypes.Transaction]
	mined  atomic.Int64 // block the receipt reports; 0 = not yet mined
	status atomic.Uint64
	head   atomic.Int64
}

func newRedeemHarness(t *testing.T) *redeemHarness {
	t.Helper()
	t.Cleanup(func() { pollInterval = 2 * time.Second })
	pollInterval = time.Millisecond

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := inmemory.New(key)
	if err != nil {
		t.Fatal(err)
	}
	h := &redeemHarness{rpc: chaintesting.NewFakeRPC(), from: crypto.PubkeyToAddress(key.PublicKey)}
	h.status.Store(ethtypes.ReceiptStatusSuccessful)
	h.rpc.DefaultNonce = 7
	h.rpc.SendTransactionFunc = func(_ context.Context, tx *ethtypes.Transaction) error {
		h.sent.Store(tx)
		return nil
	}
	h.rpc.TransactionReceiptFunc = func(_ context.Context, hash cchain.TxHash) (*ethtypes.Receipt, error) {
		sent := h.sent.Load()
		mined := h.mined.Load()
		if sent == nil || sent.Hash() != hash || mined == 0 {
			return nil, ethereum.NotFound
		}
		return &ethtypes.Receipt{Status: h.status.Load(), BlockNumber: big.NewInt(mined), GasUsed: 21000}, nil
	}
	h.rpc.HeaderByNumberFunc = func(context.Context, *big.Int) (*ethtypes.Header, error) {
		return &ethtypes.Header{Number: big.NewInt(h.head.Load())}, nil
	}
	b, err := New(Config{
		Address:       contractAddr,
		Claimant:      claimantAddr,
		From:          h.from,
		ChainID:       big.NewInt(42161),
		RedeemGas:     123_456,
		Confirmations: 4,
	}, h.rpc, fixedGas{big.NewInt(1_000)}, signer)
	if err != nil {
		t.Fatal(err)
	}
	h.broker = b
	return h
}

func TestRedeem_SubmitsSignedTxAndWaitsForConfirmations(t *testing.T) {
	h := newRedeemHarness(t)
	h.mined.Store(100)
	h.head.Store(104) // exactly Confirmations deep

	got, err := h.broker.RedeemWinningTicket(context.Background(), sampleTicket(), make([]byte, 65), big.NewInt(9))
	if err != nil {
		t.Fatalf("RedeemWinningTicket: %v", err)
	}
	tx := h.sent.Load()
	if tx == nil {
		t.Fatal("no transaction was sent")
	}
	if !bytes.Equal(got, tx.Hash().Bytes()) {
		t.Errorf("returned hash %x != sent %x", got, tx.Hash())
	}
	if tx.Nonce() != 7 || tx.Gas() != 123_456 || tx.GasPrice().Int64() != 1_000 || *tx.To() != contractAddr {
		t.Errorf("tx fields: nonce=%d gas=%d price=%s to=%s", tx.Nonce(), tx.Gas(), tx.GasPrice(), tx.To())
	}
	if !bytes.Equal(tx.Data()[:4], ParsedABI.Methods["redeemWinningTicket"].ID) {
		t.Errorf("calldata selector %x is not redeemWinningTicket", tx.Data()[:4])
	}
	signerOf, err := ethtypes.Sender(ethtypes.LatestSignerForChainID(big.NewInt(42161)), tx)
	if err != nil || signerOf != h.from {
		t.Errorf("tx not signed by the configured key: %s, %v", signerOf, err)
	}
	// Second redemption reuses the in-process counter: nonce 8, and no
	// second PendingNonceAt round-trip.
	if _, err := h.broker.RedeemWinningTicket(context.Background(), sampleTicket(), make([]byte, 65), big.NewInt(9)); err != nil {
		t.Fatal(err)
	}
	if h.sent.Load().Nonce() != 8 || h.rpc.CallCount("PendingNonceAt") != 1 {
		t.Errorf("nonce=%d pendingNonceAt calls=%d; want 8 and 1", h.sent.Load().Nonce(), h.rpc.CallCount("PendingNonceAt"))
	}
}

// A send that fails on the RPC surfaces the error and re-primes the
// nonce from the chain on the next attempt, so a counter that drifted
// during the failure cannot be reused.
func TestRedeem_SendFailureReprimesNonce(t *testing.T) {
	h := newRedeemHarness(t)
	h.mined.Store(100)
	h.head.Store(110)
	h.rpc.InjectError("SendTransaction", errors.New("connection refused"))

	_, err := h.broker.RedeemWinningTicket(context.Background(), sampleTicket(), make([]byte, 65), big.NewInt(9))
	if err == nil || !strings.Contains(err.Error(), "send tx") {
		t.Fatalf("err = %v; want send failure", err)
	}
	h.rpc.DefaultNonce = 20 // chain moved on while we were down
	if _, err := h.broker.RedeemWinningTicket(context.Background(), sampleTicket(), make([]byte, 65), big.NewInt(9)); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if h.rpc.CallCount("PendingNonceAt") != 2 || h.sent.Load().Nonce() != 20 {
		t.Errorf("pendingNonceAt calls=%d nonce=%d; want 2 and 20", h.rpc.CallCount("PendingNonceAt"), h.sent.Load().Nonce())
	}
	h.rpc.InjectError("PendingNonceAt", errors.New("down"))
	h.broker.noncePrimed = false
	if _, err := h.broker.RedeemWinningTicket(context.Background(), sampleTicket(), make([]byte, 65), big.NewInt(9)); err == nil || !strings.Contains(err.Error(), "get nonce") {
		t.Errorf("nonce error: %v", err)
	}
}

func TestRedeem_RevertedReceiptIsErrTxFailed(t *testing.T) {
	h := newRedeemHarness(t)
	h.mined.Store(100)
	h.head.Store(200)
	h.status.Store(ethtypes.ReceiptStatusFailed)
	_, err := h.broker.RedeemWinningTicket(context.Background(), sampleTicket(), make([]byte, 65), big.NewInt(9))
	if !errors.Is(err, ErrTxFailed) {
		t.Fatalf("err = %v; want ErrTxFailed", err)
	}
}

func TestRedeem_WaitsForReceiptThenConfirmations(t *testing.T) {
	h := newRedeemHarness(t)
	h.head.Store(100)
	done := make(chan error, 1)
	go func() {
		_, err := h.broker.RedeemWinningTicket(context.Background(), sampleTicket(), make([]byte, 65), big.NewInt(9))
		done <- err
	}()
	// Not mined yet: the call must still be waiting.
	time.Sleep(20 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("returned before the receipt existed: %v", err)
	default:
	}
	h.mined.Store(100) // mined, but head is only 100: 0 confirmations
	time.Sleep(20 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("returned before confirmations: %v", err)
	default:
	}
	h.head.Store(104)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("redeem: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("redeem did not return after confirmations")
	}
	if h.rpc.CallCount("TransactionReceipt") < 2 || h.rpc.CallCount("HeaderByNumber") < 2 {
		t.Errorf("expected polling: receipt=%d head=%d", h.rpc.CallCount("TransactionReceipt"), h.rpc.CallCount("HeaderByNumber"))
	}
}

func TestRedeem_ReceiptAndHeadErrorsSurface(t *testing.T) {
	h := newRedeemHarness(t)
	h.mined.Store(100)
	h.head.Store(200)
	h.rpc.InjectError("TransactionReceipt", errors.New("boom"))
	if _, err := h.broker.RedeemWinningTicket(context.Background(), sampleTicket(), make([]byte, 65), big.NewInt(9)); err == nil || !strings.Contains(err.Error(), "get receipt") {
		t.Errorf("receipt error: %v", err)
	}
	h.rpc.InjectError("HeaderByNumber", errors.New("boom"))
	if _, err := h.broker.RedeemWinningTicket(context.Background(), sampleTicket(), make([]byte, 65), big.NewInt(9)); err == nil || !strings.Contains(err.Error(), "head header") {
		t.Errorf("head error: %v", err)
	}
}

func TestRedeem_ContextCancelWhileWaiting(t *testing.T) {
	h := newRedeemHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := h.broker.RedeemWinningTicket(ctx, sampleTicket(), make([]byte, 65), big.NewInt(9))
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v; want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not unblock the wait")
	}
}

func TestToSolTicket_AuxDataLayout(t *testing.T) {
	tk := sampleTicket()
	tk.CreationRound = 12345
	tk.CreationRoundHash = bytes.Repeat([]byte{0x70}, 32)
	st := toSolTicket(tk)
	if got, want := len(st.AuxData), 64; got != want {
		t.Errorf("AuxData length = %d; want %d", got, want)
	}
	// First 32 bytes: zero-padded round (12345 == 0x3039).
	if st.AuxData[31] != 0x39 || st.AuxData[30] != 0x30 {
		t.Errorf("AuxData round-encoded bytes: %x", st.AuxData[:32])
	}
	// Last 32 bytes: the 0x70-filled hash.
	if st.AuxData[63] != 0x70 {
		t.Errorf("AuxData hash byte: %x", st.AuxData[32:])
	}
	if st.Recipient != claimantAddr || st.Sender != senderAddr {
		t.Errorf("addresses: %+v", st)
	}
}

func TestToSolTicket_AuxDataEmpty(t *testing.T) {
	tk := sampleTicket()
	tk.CreationRoundHash = make([]byte, 32)
	if st := toSolTicket(tk); len(st.AuxData) != 0 {
		t.Errorf("AuxData should be empty when both fields zero; got %d bytes", len(st.AuxData))
	}
}

func TestLeftPad32(t *testing.T) {
	if got := leftPad32([]byte{1, 2}); len(got) != 32 || got[31] != 2 || got[30] != 1 {
		t.Errorf("short: %x", got)
	}
	long := bytes.Repeat([]byte{9}, 40)
	if got := leftPad32(long); len(got) != 32 {
		t.Errorf("long: %d bytes", len(got))
	}
	if nilToZero(nil).Sign() != 0 {
		t.Error("nilToZero(nil) must be zero")
	}
}
