package escrow

import (
	"context"
	"errors"
	"math/big"
	"path/filepath"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
)

type brokerFixture struct {
	info *providers.SenderInfo
	err  error
}

func (b brokerFixture) GetSenderInfo(context.Context, []byte) (*providers.SenderInfo, error) {
	return b.info, b.err
}
func (brokerFixture) IsUsedTicket(context.Context, []byte) (bool, error) { return false, nil }
func (brokerFixture) RedeemWinningTicket(context.Context, *providers.Ticket, []byte, *big.Int) ([]byte, error) {
	return nil, nil
}
func (brokerFixture) TicketValidityPeriod(context.Context) (int64, error) { return 2, nil }

type clockFixture struct{ pool *big.Int }

func (clockFixture) LastInitializedRound() int64        { return 1 }
func (clockFixture) LastInitializedL1BlockHash() []byte { return nil }
func (clockFixture) LastSeenL1Block() *big.Int          { return big.NewInt(1) }
func (c clockFixture) GetTranscoderPoolSize() *big.Int  { return c.pool }

func TestAvailableFundsAndProviderErrors(t *testing.T) {
	claimant := []byte{0xaa}
	info := &providers.SenderInfo{
		Deposit: big.NewInt(30),
		Reserve: &providers.Reserve{FundsRemaining: big.NewInt(90), Claimed: map[string]*big.Int{"0xaa": big.NewInt(10)}},
	}
	e := New(brokerFixture{info: info}, clockFixture{pool: big.NewInt(10)}, Config{Claimant: claimant})
	e.SubFloat([]byte("payer"), big.NewInt(7))
	got, err := e.AvailableFunds(context.Background(), []byte("payer"))
	// reserve = (90+10)/10 - 10 = 0, then deposit 30 - pending 7.
	if err != nil || got.Int64() != 23 {
		t.Fatalf("available=%v err=%v", got, err)
	}
	boom := errors.New("chain unavailable")
	failing := New(brokerFixture{err: boom}, clockFixture{pool: big.NewInt(1)}, Config{})
	if _, err := failing.MaxFloat(context.Background(), nil); !errors.Is(err, boom) {
		t.Fatalf("MaxFloat err=%v", err)
	}
	if _, err := failing.AvailableFunds(context.Background(), nil); !errors.Is(err, boom) {
		t.Fatalf("AvailableFunds err=%v", err)
	}
}

func TestEscrowNoopsAndReserveEdges(t *testing.T) {
	e := New(brokerFixture{}, clockFixture{}, Config{})
	e.SubFloat([]byte("payer"), nil)
	e.SubFloat([]byte("payer"), big.NewInt(0))
	if err := e.AddFloat([]byte("payer"), nil); err != nil {
		t.Fatal(err)
	}
	if e.Pending([]byte("payer")).Sign() != 0 {
		t.Fatal("no-op operations changed pending")
	}
	if e.reserveAlloc(nil).Sign() != 0 || e.reserveAlloc(&providers.SenderInfo{}).Sign() != 0 {
		t.Fatal("missing reserve allocated funds")
	}
	if got := nilToZero(nil); got.Sign() != 0 {
		t.Fatalf("nilToZero=%s", got)
	}
}

func TestRebuildPendingRedemptions(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "payee.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sender := []byte{0x01, 0x02}
	for i, face := range []*big.Int{big.NewInt(11), big.NewInt(13), nil} {
		hash := make([]byte, 32)
		hash[31] = byte(i + 1)
		if _, err := st.EnqueueRedemption(hash, &store.SignedTicket{Sender: sender, FaceValue: face}); err != nil {
			t.Fatal(err)
		}
	}
	e := New(brokerFixture{}, clockFixture{}, Config{})
	if err := e.Rebuild(st); err != nil {
		t.Fatal(err)
	}
	if got := e.Pending(sender); got.Int64() != 24 {
		t.Fatalf("rebuilt pending=%s, want 24", got)
	}
}
