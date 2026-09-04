package oracle

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/gasoracle"
	chaintesting "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
)

func estimate(wei int64) gasoracle.Estimate {
	return gasoracle.Estimate{BaseFee: big.NewInt(wei), TipCap: big.NewInt(1), FeeCap: big.NewInt(wei * 3)}
}

func TestGasPrice_AppliesMultiplier(t *testing.T) {
	o := chaintesting.NewFakeGasOracle()
	o.SetEstimate(estimate(100))
	g, err := NewWithOracle(context.Background(), Config{MultiplierPct: 200, RefreshInterval: time.Hour}, o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := g.Current(), big.NewInt(200); got.Cmp(want) != 0 {
		t.Errorf("Current = %s; want %s (100 * 200 / 100)", got, want)
	}
	// Current returns a copy.
	g.Current().SetInt64(0)
	if g.Current().Int64() != 200 {
		t.Error("Current returned an aliased value")
	}
}

func TestGasPrice_DefaultMultiplierAndInterval(t *testing.T) {
	o := chaintesting.NewFakeGasOracle()
	o.SetEstimate(estimate(16))
	g, err := NewWithOracle(context.Background(), Config{}, o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := g.Current(), big.NewInt(32); got.Cmp(want) != 0 {
		t.Errorf("Current = %s; want %s", got, want)
	}
	if g.cfg.RefreshInterval != 5*time.Second {
		t.Errorf("default refresh = %s; want 5s", g.cfg.RefreshInterval)
	}
}

// The runbook's promise: the value is eth_gasPrice × multiplier, read
// through chain-commons. Build over a real TTL oracle on a FakeRPC.
func TestGasPrice_OverRPCUsesSuggestGasPrice(t *testing.T) {
	f := chaintesting.NewFakeRPC()
	f.SuggestGasPriceFunc = func(context.Context) (*big.Int, error) { return big.NewInt(1_000), nil }
	g, err := New(context.Background(), Config{MultiplierPct: 150, RefreshInterval: time.Hour}, f)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := g.Current().Int64(); got != 1_500 {
		t.Errorf("Current = %d; want 1500", got)
	}
	if f.CallCount("SuggestGasPrice") != 1 {
		t.Errorf("SuggestGasPrice calls = %d; want exactly one per refresh", f.CallCount("SuggestGasPrice"))
	}
	if _, err := New(context.Background(), Config{}, nil); err == nil {
		t.Error("nil rpc must error")
	}
}

func TestGasPrice_InitialSyncFailureAborts(t *testing.T) {
	o := chaintesting.NewFakeGasOracle()
	o.FailNextSuggest(errors.New("rpc down"))
	if _, err := NewWithOracle(context.Background(), Config{}, o); err == nil || !strings.Contains(err.Error(), "initial sync") {
		t.Fatalf("err = %v", err)
	}
	o.SetEstimate(gasoracle.Estimate{BaseFee: big.NewInt(0)})
	if _, err := NewWithOracle(context.Background(), Config{}, o); err == nil || !strings.Contains(err.Error(), "non-positive") {
		t.Fatalf("zero gas price: err = %v", err)
	}
	if _, err := NewWithOracle(context.Background(), Config{}, nil); err == nil {
		t.Error("nil oracle must error")
	}
}

// A failed refresh keeps the last good value; the next one recovers.
func TestGasPrice_RefreshFailureKeepsLastGood(t *testing.T) {
	o := chaintesting.NewFakeGasOracle()
	o.SetEstimate(estimate(100))
	g, err := NewWithOracle(context.Background(), Config{MultiplierPct: 100}, o)
	if err != nil {
		t.Fatal(err)
	}
	o.FailNextSuggest(errors.New("rpc down"))
	if err := g.refresh(context.Background()); err == nil {
		t.Fatal("expected refresh error")
	}
	if g.Current().Int64() != 100 {
		t.Fatalf("failed refresh must keep last good, got %s", g.Current())
	}
	o.SetEstimate(estimate(250))
	if err := g.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g.Current().Int64() != 250 {
		t.Fatalf("recovered value = %s; want 250", g.Current())
	}
}

func TestGasPrice_StartRefreshesAndStopIsIdempotent(t *testing.T) {
	o := chaintesting.NewFakeGasOracle()
	o.SetEstimate(estimate(100))
	g, err := NewWithOracle(context.Background(), Config{MultiplierPct: 100, RefreshInterval: 5 * time.Millisecond}, o)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g.Start(ctx)
	o.SetEstimate(estimate(900))
	deadline := time.Now().Add(2 * time.Second)
	for g.Current().Int64() != 900 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if g.Current().Int64() != 900 {
		t.Fatal("refresh loop never observed the new price")
	}
	g.Stop()
	g.Stop()
}

func TestGasPrice_ZeroBeforeSync(t *testing.T) {
	g := &GasPrice{}
	if g.Current().Sign() != 0 {
		t.Fatal("unsynced provider must report zero")
	}
}
