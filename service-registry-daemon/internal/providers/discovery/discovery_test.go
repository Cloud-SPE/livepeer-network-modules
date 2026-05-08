package discovery

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	ccbm "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/bondingmanager"
	chaintesting "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var (
	selectorGetFirstInPool = crypto.Keccak256([]byte("getFirstTranscoderInPool()"))[:4]
	selectorGetNextInPool  = crypto.Keccak256([]byte("getNextTranscoderInPool(address)"))[:4]
)

func TestChain_ActiveOrchs_EmptyPool(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return ccbm.EncodeAddressSlot(chain.Address{}), nil
	}
	d, err := NewChain(r, common.HexToAddress("0x000000000000000000000000000000000000FB01"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.ActiveOrchs(context.Background())
	if err != nil {
		t.Fatalf("ActiveOrchs: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty pool, got %d entries", len(got))
	}
}

func TestChain_ActiveOrchs_WalksPool(t *testing.T) {
	first := common.HexToAddress("0x00000000000000000000000000000000000000AA")
	second := common.HexToAddress("0x00000000000000000000000000000000000000BB")
	third := common.HexToAddress("0x00000000000000000000000000000000000000CC")

	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		switch {
		case len(msg.Data) >= 4 && bytes.Equal(msg.Data[:4], selectorGetFirstInPool):
			return ccbm.EncodeAddressSlot(first), nil
		case len(msg.Data) >= 4 && bytes.Equal(msg.Data[:4], selectorGetNextInPool):
			var arg chain.Address
			copy(arg[:], msg.Data[4+12:4+32])
			switch arg {
			case first:
				return ccbm.EncodeAddressSlot(second), nil
			case second:
				return ccbm.EncodeAddressSlot(third), nil
			default:
				return ccbm.EncodeAddressSlot(chain.Address{}), nil
			}
		}
		return nil, errors.New("unexpected call")
	}

	d, err := NewChain(r, common.HexToAddress("0x000000000000000000000000000000000000FB01"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.ActiveOrchs(context.Background())
	if err != nil {
		t.Fatalf("ActiveOrchs: %v", err)
	}
	want := []types.EthAddress{
		types.EthAddress(toLower(first.Hex())),
		types.EthAddress(toLower(second.Hex())),
		types.EthAddress(toLower(third.Hex())),
	}
	if len(got) != len(want) {
		t.Fatalf("ActiveOrchs returned %d entries; want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry[%d] = %s; want %s", i, got[i], want[i])
		}
	}
}

func TestChain_ActiveOrchs_FirstError(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return nil, errors.New("rpc down")
	}
	d, _ := NewChain(r, common.HexToAddress("0x000000000000000000000000000000000000FB01"))
	if _, err := d.ActiveOrchs(context.Background()); err == nil {
		t.Fatal("expected error on RPC failure")
	}
}

func TestDisabled_ActiveOrchs_ReturnsEmpty(t *testing.T) {
	d := NewDisabled()
	got, err := d.ActiveOrchs(context.Background())
	if err != nil {
		t.Fatalf("ActiveOrchs: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Disabled.ActiveOrchs returned %d; want 0", len(got))
	}
}

// toLower is a tiny ASCII lower-case for hex strings so we can compare
// against types.EthAddress (which canonicalizes to lower).
func toLower(s string) string {
	out := make([]byte, len(s))
	for i, c := range []byte(s) {
		if c >= 'A' && c <= 'F' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
