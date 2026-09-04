package ticketbroker

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum"
	ethcommon "github.com/ethereum/go-ethereum/common"

	cerrors "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/errors"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"
	chaintesting "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/types"
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
	b, err := New(Config{Address: contractAddr, Claimant: claimantAddr}, f, nil)
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
	b, _ := New(Config{Address: contractAddr}, f, nil)
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
	b, _ := New(Config{Address: contractAddr}, f, nil)
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
	b, _ := New(Config{Address: contractAddr}, f, nil)
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
	b, _ := New(Config{Address: contractAddr}, f, nil)
	got, err := b.TicketValidityPeriod(context.Background())
	if err != nil || got != 2 {
		t.Fatalf("got %d, %v; want 2", got, err)
	}
	f = contractStub(t, map[string][]byte{"ticketValidityPeriod": abiOut(t, "ticketValidityPeriod", big.NewInt(0))})
	b, _ = New(Config{Address: contractAddr}, f, nil)
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
	if _, err := New(Config{Address: contractAddr}, nil, nil); err == nil {
		t.Error("nil client must error")
	}
	if _, err := New(Config{}, f, nil); err == nil {
		t.Error("empty address must error")
	}
	b, err := New(Config{Address: contractAddr}, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.cfg.RedeemGas != defaultRedeemGas {
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

func TestTicketHash_MatchesTypes(t *testing.T) {
	tk := sampleTicket()
	tk.CreationRound = 7
	tk.CreationRoundHash = bytes.Repeat([]byte{0x11}, 32)
	want := (&types.Ticket{
		Recipient: tk.Recipient, Sender: tk.Sender, FaceValue: tk.FaceValue, WinProb: tk.WinProb,
		SenderNonce: tk.SenderNonce, RecipientRandHash: tk.RecipientRandHash,
		CreationRound: tk.CreationRound, CreationRoundHash: tk.CreationRoundHash,
	}).Hash()
	if got := TicketHash(tk); !bytes.Equal(got, want) || len(got) != 32 {
		t.Fatalf("TicketHash = %x, want %x", got, want)
	}
}

func TestRedeem_RejectsMisconfiguration(t *testing.T) {
	f := chaintesting.NewFakeRPC()
	ctx := context.Background()
	b, _ := New(Config{Address: contractAddr}, f, nil)
	if _, err := b.RedeemWinningTicket(ctx, sampleTicket(), make([]byte, 65), big.NewInt(1)); err == nil || !strings.Contains(err.Error(), "intent manager") {
		t.Errorf("read-only broker: %v", err)
	}
	if _, err := b.RedeemWinningTicket(ctx, nil, nil, big.NewInt(1)); err == nil {
		t.Error("nil ticket must error")
	}
	if _, err := b.RedeemWinningTicket(ctx, sampleTicket(), nil, nil); err == nil {
		t.Error("nil recipientRand must error")
	}
}

// fakeIntents scripts the four Manager calls the broker makes.
type fakeIntents struct {
	mu        sync.Mutex
	submitted []txintent.Params
	resubmits [][]byte
	submitErr error
	statusFn  func(id txintent.IntentID) (txintent.TxIntent, error)
	resubErr  error
	waitFn    func(ctx context.Context, id txintent.IntentID) (txintent.TxIntent, error)
	waits     int
}

func (f *fakeIntents) Submit(_ context.Context, p txintent.Params) (txintent.IntentID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.submitErr != nil {
		return txintent.IntentID{}, f.submitErr
	}
	f.submitted = append(f.submitted, p)
	return txintent.ComputeID(p.Kind, p.KeyParams), nil
}

func (f *fakeIntents) Status(_ context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
	if f.statusFn != nil {
		return f.statusFn(id)
	}
	return txintent.TxIntent{ID: id, Status: txintent.StatusPending}, nil
}

func (f *fakeIntents) Resubmit(_ context.Context, _ txintent.IntentID, calldata []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resubmits = append(f.resubmits, calldata)
	return f.resubErr
}

func (f *fakeIntents) Wait(ctx context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
	f.mu.Lock()
	f.waits++
	f.mu.Unlock()
	if f.waitFn != nil {
		return f.waitFn(ctx, id)
	}
	return confirmed(id, txHashA), nil
}

var txHashA = ethcommon.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

func confirmed(id txintent.IntentID, h ethcommon.Hash) txintent.TxIntent {
	return txintent.TxIntent{ID: id, Status: txintent.StatusConfirmed,
		Attempts: []txintent.IntentAttempt{{Nonce: 3, SignedTxHash: h}}}
}

func failed(id txintent.IntentID, class cerrors.ErrorClass, code string) txintent.TxIntent {
	return txintent.TxIntent{ID: id, Status: txintent.StatusFailed,
		Attempts:     []txintent.IntentAttempt{{Nonce: 3, SignedTxHash: txHashA}},
		FailedReason: cerrors.New(class, code, "scripted")}
}

func notUsed(t *testing.T) *chaintesting.FakeRPC {
	return contractStub(t, map[string][]byte{"usedTickets": abiOut(t, "usedTickets", false)})
}

func newIntentBroker(t *testing.T, rpc *chaintesting.FakeRPC, fi *fakeIntents) *Broker {
	t.Helper()
	b, err := New(Config{Address: contractAddr, Claimant: claimantAddr, RedeemGas: 123_456}, rpc, fi)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRedeem_UsedPrecheckSkipsIntent(t *testing.T) {
	fi := &fakeIntents{submitErr: errors.New("must not submit")}
	b := newIntentBroker(t, contractStub(t, map[string][]byte{"usedTickets": abiOut(t, "usedTickets", true)}), fi)
	_, err := b.RedeemWinningTicket(context.Background(), sampleTicket(), make([]byte, 65), big.NewInt(1))
	if !errors.Is(err, providers.ErrTicketAlreadyUsed) {
		t.Fatalf("err = %v, want ErrTicketAlreadyUsed", err)
	}
	if len(fi.submitted) != 0 || fi.waits != 0 {
		t.Fatalf("used ticket reached the intent manager: %+v", fi)
	}
}

func TestRedeem_PrecheckErrorSurfaces(t *testing.T) {
	f := chaintesting.NewFakeRPC()
	f.CallContractFunc = func(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error) { return nil, errors.New("rpc down") }
	b := newIntentBroker(t, f, &fakeIntents{})
	_, err := b.RedeemWinningTicket(context.Background(), sampleTicket(), make([]byte, 65), big.NewInt(1))
	if err == nil || !strings.Contains(err.Error(), "pre-check") {
		t.Fatalf("err = %v", err)
	}
}

func TestRedeem_SubmitsIntentAndReturnsConfirmedHash(t *testing.T) {
	fi := &fakeIntents{}
	b := newIntentBroker(t, notUsed(t), fi)
	tk := sampleTicket()
	sig := bytes.Repeat([]byte{7}, 65)
	got, err := b.RedeemWinningTicket(context.Background(), tk, sig, big.NewInt(99))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, txHashA.Bytes()) {
		t.Fatalf("tx hash = %x", got)
	}
	if len(fi.submitted) != 1 {
		t.Fatalf("submits = %d", len(fi.submitted))
	}
	p := fi.submitted[0]
	if p.Kind != IntentKind || !bytes.Equal(p.KeyParams, TicketHash(tk)) || p.To != contractAddr || p.GasLimit != 123_456 {
		t.Fatalf("params = %+v", p)
	}
	if p.Metadata["ticket_hash"] != ethcommon.Bytes2Hex(TicketHash(tk)) {
		t.Fatalf("metadata = %v", p.Metadata)
	}
	// The calldata is the real redeemWinningTicket encoding.
	m := ParsedABI.Methods["redeemWinningTicket"]
	if !bytes.Equal(p.CallData[:4], m.ID) {
		t.Fatalf("selector = %x", p.CallData[:4])
	}
	args, err := m.Inputs.Unpack(p.CallData[4:])
	if err != nil {
		t.Fatal(err)
	}
	if got := args[2].(*big.Int); got.Int64() != 99 {
		t.Fatalf("recipientRand = %s", got)
	}
	if got := args[1].([]byte); !bytes.Equal(got, sig) {
		t.Fatalf("sig = %x", got)
	}
	if fi.waits != 1 || len(fi.resubmits) != 0 {
		t.Fatalf("waits=%d resubmits=%d", fi.waits, len(fi.resubmits))
	}
}

func TestRedeem_ExistingRevertedIntentIsTxFailed(t *testing.T) {
	fi := &fakeIntents{statusFn: func(id txintent.IntentID) (txintent.TxIntent, error) {
		return failed(id, cerrors.ClassReverted, "tx.reverted"), nil
	}}
	b := newIntentBroker(t, notUsed(t), fi)
	_, err := b.RedeemWinningTicket(context.Background(), sampleTicket(), make([]byte, 65), big.NewInt(1))
	if !errors.Is(err, ErrTxFailed) || !errors.Is(err, providers.ErrRedemptionReverted) {
		t.Fatalf("err = %v, want ErrTxFailed", err)
	}
	if cerrors.Classify(err).Class != cerrors.ClassReverted {
		t.Fatalf("class = %s", cerrors.Classify(err).Class)
	}
	if !strings.Contains(err.Error(), txHashA.Hex()) {
		t.Fatalf("revert error should name the tx: %v", err)
	}
	if len(fi.resubmits) != 0 || fi.waits != 0 {
		t.Fatalf("a reverted intent must not be re-driven: %+v", fi)
	}
}

func TestRedeem_ExistingTransientFailureIsResubmitted(t *testing.T) {
	fi := &fakeIntents{statusFn: func(id txintent.IntentID) (txintent.TxIntent, error) {
		return failed(id, cerrors.ClassTransient, "tx.replacement_exhausted"), nil
	}}
	b := newIntentBroker(t, notUsed(t), fi)
	got, err := b.RedeemWinningTicket(context.Background(), sampleTicket(), make([]byte, 65), big.NewInt(1))
	if err != nil || !bytes.Equal(got, txHashA.Bytes()) {
		t.Fatalf("got %x, %v", got, err)
	}
	if len(fi.resubmits) != 1 || !bytes.Equal(fi.resubmits[0], fi.submitted[0].CallData) {
		t.Fatalf("resubmit not issued with the calldata: %+v", fi.resubmits)
	}
}

func TestRedeem_ManagerErrorsSurface(t *testing.T) {
	ctx := context.Background()
	tk := sampleTicket()
	sig := make([]byte, 65)

	b := newIntentBroker(t, notUsed(t), &fakeIntents{submitErr: errors.New("store closed")})
	if _, err := b.RedeemWinningTicket(ctx, tk, sig, big.NewInt(1)); err == nil || !strings.Contains(err.Error(), "submit redemption intent") {
		t.Errorf("submit: %v", err)
	}

	b = newIntentBroker(t, notUsed(t), &fakeIntents{statusFn: func(txintent.IntentID) (txintent.TxIntent, error) {
		return txintent.TxIntent{}, errors.New("read failed")
	}})
	if _, err := b.RedeemWinningTicket(ctx, tk, sig, big.NewInt(1)); err == nil || !strings.Contains(err.Error(), "intent status") {
		t.Errorf("status: %v", err)
	}

	b = newIntentBroker(t, notUsed(t), &fakeIntents{
		statusFn: func(id txintent.IntentID) (txintent.TxIntent, error) {
			return failed(id, cerrors.ClassPermanent, "x"), nil
		},
		resubErr: errors.New("not failed"),
	})
	if _, err := b.RedeemWinningTicket(ctx, tk, sig, big.NewInt(1)); err == nil || !strings.Contains(err.Error(), "resubmit") {
		t.Errorf("resubmit: %v", err)
	}
}

func TestRedeem_WaitOutcomes(t *testing.T) {
	ctx := context.Background()
	tk := sampleTicket()
	sig := make([]byte, 65)
	redeem := func(fn func(context.Context, txintent.IntentID) (txintent.TxIntent, error)) error {
		b := newIntentBroker(t, notUsed(t), &fakeIntents{waitFn: fn})
		_, err := b.RedeemWinningTicket(ctx, tk, sig, big.NewInt(1))
		return err
	}

	// Cancelled wait: the intent keeps going; the caller sees ctx.Err.
	err := redeem(func(context.Context, txintent.IntentID) (txintent.TxIntent, error) {
		return txintent.TxIntent{}, context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancel: %v", err)
	}

	// Reverted after the wait.
	err = redeem(func(_ context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
		return failed(id, cerrors.ClassReverted, "tx.reverted"), nil
	})
	if !errors.Is(err, ErrTxFailed) {
		t.Errorf("revert: %v", err)
	}

	// Failed for a non-revert reason keeps the classification and is
	// not ErrTxFailed.
	err = redeem(func(_ context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
		return failed(id, cerrors.ClassTransient, "tx.replacement_exhausted"), nil
	})
	if errors.Is(err, ErrTxFailed) || cerrors.Classify(err).Class != cerrors.ClassTransient {
		t.Errorf("transient: %v (class %s)", err, cerrors.Classify(err).Class)
	}

	// Failed without a recorded reason still fails loudly.
	err = redeem(func(_ context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
		return txintent.TxIntent{ID: id, Status: txintent.StatusFailed}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "without a recorded reason") {
		t.Errorf("no reason: %v", err)
	}

	// Confirmed without an attempt is a bug, not a success.
	err = redeem(func(_ context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
		return txintent.TxIntent{ID: id, Status: txintent.StatusConfirmed}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "without an attempt") {
		t.Errorf("no attempt: %v", err)
	}

	// A non-terminal state out of Wait is a contract violation.
	err = redeem(func(_ context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
		return txintent.TxIntent{ID: id, Status: txintent.StatusSubmitted}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "non-terminal") {
		t.Errorf("non-terminal: %v", err)
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
