package chaintesting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/controller"
)

func TestFakeController_AddressesReturnsInitial(t *testing.T) {
	initial := controller.Addresses{
		RoundsManager:  chain.Address{0x01},
		BondingManager: chain.Address{0x02},
	}
	c := NewFakeController(initial, nil)
	got := c.Addresses()
	if got.RoundsManager != initial.RoundsManager {
		t.Errorf("RoundsManager = %v, want %v", got.RoundsManager, initial.RoundsManager)
	}
	if got.BondingManager != initial.BondingManager {
		t.Errorf("BondingManager = %v", got.BondingManager)
	}
}

func TestFakeController_SetAddressNotifiesSubscriber(t *testing.T) {
	c := NewFakeController(controller.Addresses{}, nil)
	sub := c.Subscribe()

	c.SetAddress("RoundsManager", chain.Address{0xff})

	select {
	case got := <-sub:
		if got.RoundsManager != (chain.Address{0xff}) {
			t.Errorf("subscriber received %v", got.RoundsManager)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive notification")
	}
}

func TestFakeController_RefreshNoFunc(t *testing.T) {
	c := NewFakeController(controller.Addresses{}, nil)
	if err := c.Refresh(context.Background()); err != nil {
		t.Errorf("Refresh with no func = %v, want nil", err)
	}
}

func TestFakeController_RefreshFunc(t *testing.T) {
	c := NewFakeController(controller.Addresses{}, nil)
	want := chain.Address{0xab}
	c.SetRefreshFunc(func(_ context.Context) (controller.Addresses, error) {
		return controller.Addresses{RoundsManager: want}, nil
	})
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := c.Addresses().RoundsManager; got != want {
		t.Errorf("after Refresh, RoundsManager = %v, want %v", got, want)
	}
}

func TestFakeController_RefreshFuncErr(t *testing.T) {
	c := NewFakeController(controller.Addresses{RoundsManager: chain.Address{0x01}}, nil)
	want := errors.New("controller down")
	c.SetRefreshFunc(func(_ context.Context) (controller.Addresses, error) {
		return controller.Addresses{}, want
	})
	if err := c.Refresh(context.Background()); err != want {
		t.Errorf("Refresh err = %v, want %v", err, want)
	}
	// Last-good preserved.
	if got := c.Addresses().RoundsManager; got != (chain.Address{0x01}) {
		t.Errorf("Refresh failure should preserve last-good address, got %v", got)
	}
}

func TestFakeController_NoNotifyOnNoChange(t *testing.T) {
	c := NewFakeController(controller.Addresses{RoundsManager: chain.Address{0x01}}, nil)
	sub := c.Subscribe()

	c.SetRefreshFunc(func(_ context.Context) (controller.Addresses, error) {
		return controller.Addresses{RoundsManager: chain.Address{0x01}}, nil // unchanged
	})
	_ = c.Refresh(context.Background())

	select {
	case <-sub:
		t.Errorf("subscriber should NOT receive notification on no-change refresh")
	case <-time.After(50 * time.Millisecond):
		// good
	}
}
