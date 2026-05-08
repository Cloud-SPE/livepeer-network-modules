package eth_test

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	cethctrl "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/controller/eth"
	chaintest "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/crypto"
)

// fixtureRPC returns a FakeRPC that responds to CallContract for
// getContract(bytes32) by mapping keccak256(name) → per-name address.
// Names not in the map return the zero address.
func fixtureRPC(addrs map[string]chain.Address) *chaintest.FakeRPC {
	rpc := chaintest.NewFakeRPC()
	rpc.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		if len(msg.Data) < 36 {
			return nil, errors.New("bad calldata")
		}
		nameHash := msg.Data[4:36]
		for name, addr := range addrs {
			if bytes.Equal(crypto.Keccak256([]byte(name)), nameHash) {
				return cethctrl.AbiEncodeAddress(addr), nil
			}
		}
		return cethctrl.AbiEncodeAddress(chain.Address{}), nil
	}
	return rpc
}

func TestNew_RequiresRPC(t *testing.T) {
	_, err := cethctrl.New(context.Background(), cethctrl.Options{})
	if err == nil {
		t.Errorf("New without RPC should fail")
	}
}

func TestNew_RequiresControllerAddrUnlessSkipped(t *testing.T) {
	rpc := fixtureRPC(nil)
	_, err := cethctrl.New(context.Background(), cethctrl.Options{RPC: rpc})
	if err == nil {
		t.Errorf("New without ControllerAddr should fail")
	}
}

func TestNew_ResolvesAllNamedContracts(t *testing.T) {
	want := map[string]chain.Address{
		"RoundsManager":   {0x01},
		"BondingManager":  {0x02},
		"Minter":          {0x03},
		"TicketBroker":    {0x04},
		"ServiceRegistry": {0x05},
		"LivepeerToken":   {0x06},
	}
	rpc := fixtureRPC(want)

	c, err := cethctrl.New(context.Background(), cethctrl.Options{
		RPC:            rpc,
		ControllerAddr: chain.Address{0xff},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.(closeable).Close()

	addrs := c.Addresses()
	if addrs.RoundsManager != want["RoundsManager"] {
		t.Errorf("RoundsManager = %v, want %v", addrs.RoundsManager, want["RoundsManager"])
	}
	if addrs.BondingManager != want["BondingManager"] {
		t.Errorf("BondingManager = %v", addrs.BondingManager)
	}
	if addrs.Minter != want["Minter"] {
		t.Errorf("Minter = %v", addrs.Minter)
	}
	if addrs.TicketBroker != want["TicketBroker"] {
		t.Errorf("TicketBroker = %v", addrs.TicketBroker)
	}
	if addrs.ServiceRegistry != want["ServiceRegistry"] {
		t.Errorf("ServiceRegistry = %v", addrs.ServiceRegistry)
	}
	if addrs.LivepeerToken != want["LivepeerToken"] {
		t.Errorf("LivepeerToken = %v", addrs.LivepeerToken)
	}
}

func TestNew_OverridesAreUsed(t *testing.T) {
	chainAddrs := map[string]chain.Address{"RoundsManager": {0x01}}
	rpc := fixtureRPC(chainAddrs)

	c, err := cethctrl.New(context.Background(), cethctrl.Options{
		RPC:            rpc,
		ControllerAddr: chain.Address{0xff},
		ContractOverrides: map[string]chain.Address{
			"RoundsManager": {0xab},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.(closeable).Close()

	if c.Addresses().RoundsManager != (chain.Address{0xab}) {
		t.Errorf("override not applied: got %v", c.Addresses().RoundsManager)
	}
}

func TestNew_SkipController_AllOverrides(t *testing.T) {
	rpc := fixtureRPC(nil)
	c, err := cethctrl.New(context.Background(), cethctrl.Options{
		RPC:            rpc,
		SkipController: true,
		ContractOverrides: map[string]chain.Address{
			"RoundsManager": {0xab},
		},
	})
	if err != nil {
		t.Fatalf("New with SkipController: %v", err)
	}
	defer c.(closeable).Close()
	if c.Addresses().RoundsManager != (chain.Address{0xab}) {
		t.Errorf("SkipController override not applied: %v", c.Addresses().RoundsManager)
	}
}

func TestRefresh_NotifiesOnChange(t *testing.T) {
	chainAddrs := map[string]chain.Address{"RoundsManager": {0x01}}
	rpc := fixtureRPC(chainAddrs)
	c, err := cethctrl.New(context.Background(), cethctrl.Options{
		RPC:            rpc,
		ControllerAddr: chain.Address{0xff},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.(closeable).Close()

	sub := c.Subscribe()
	chainAddrs["RoundsManager"] = chain.Address{0x99}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	select {
	case got := <-sub:
		if got.RoundsManager != (chain.Address{0x99}) {
			t.Errorf("subscriber received %v", got.RoundsManager)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive notification")
	}
}

func TestRefresh_NoNotifyOnNoChange(t *testing.T) {
	chainAddrs := map[string]chain.Address{"RoundsManager": {0x01}}
	rpc := fixtureRPC(chainAddrs)
	c, _ := cethctrl.New(context.Background(), cethctrl.Options{
		RPC:            rpc,
		ControllerAddr: chain.Address{0xff},
	})
	defer c.(closeable).Close()

	sub := c.Subscribe()
	_ = c.Refresh(context.Background())

	select {
	case <-sub:
		t.Errorf("subscriber should not receive notification on no-change refresh")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNew_ResolveError(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	rpc.InjectErrorN("CallContract", errors.New("rpc dead"), 100)
	_, err := cethctrl.New(context.Background(), cethctrl.Options{
		RPC:            rpc,
		ControllerAddr: chain.Address{0xff},
	})
	if err == nil {
		t.Errorf("New with broken RPC should fail")
	}
}

type closeable interface {
	Close() error
}
