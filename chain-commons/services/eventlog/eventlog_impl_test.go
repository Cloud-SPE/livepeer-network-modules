package eventlog_test

import (
	"context"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	clogs "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logs"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logs/poller"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/eventlog"
	chaintest "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
	"github.com/ethereum/go-ethereum"
)

func TestNew_RequiresLogs(t *testing.T) {
	if _, err := eventlog.New(eventlog.Options{}); err == nil {
		t.Errorf("New without Logs should fail")
	}
}

func TestSubscribe_RequiresName(t *testing.T) {
	logs := newPollerLogs(t)
	el, _ := eventlog.New(eventlog.Options{Logs: logs})
	if _, err := el.Subscribe(context.Background(), "", ethereum.FilterQuery{}); err == nil {
		t.Errorf("Subscribe with empty name should fail")
	}
}

func TestSubscribe_PassesThroughEvents(t *testing.T) {
	logs := newPollerLogs(t)
	el, _ := eventlog.New(eventlog.Options{Logs: logs})

	sub, err := el.Subscribe(context.Background(), "test", ethereum.FilterQuery{})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()
	if sub.Events() == nil {
		t.Errorf("Events channel should be non-nil")
	}
	if err := sub.Ack(chain.BlockNumber(10)); err != nil {
		t.Errorf("Ack: %v", err)
	}
}

func TestSubscribe_DuplicateNameFails(t *testing.T) {
	logs := newPollerLogs(t)
	el, _ := eventlog.New(eventlog.Options{Logs: logs})

	sub, err := el.Subscribe(context.Background(), "dup", ethereum.FilterQuery{})
	if err != nil {
		t.Fatalf("Subscribe 1: %v", err)
	}
	defer sub.Close()

	_, err = el.Subscribe(context.Background(), "dup", ethereum.FilterQuery{})
	if err == nil {
		t.Errorf("second Subscribe with same name should fail")
	}
}

// newPollerLogs returns a real logs.Logs poller wired to a FakeRPC + FakeStore.
// FakeRPC's default HeaderByNumber returns Number=0 — sufficient for these
// pass-through tests where the polling loop is dialled to once-per-hour.
func newPollerLogs(t *testing.T) clogs.Logs {
	t.Helper()
	rpc := chaintest.NewFakeRPC()
	p, err := poller.New(poller.Options{
		RPC:          rpc,
		Store:        chaintest.NewFakeStore(),
		PollInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("poller.New: %v", err)
	}
	return p
}
