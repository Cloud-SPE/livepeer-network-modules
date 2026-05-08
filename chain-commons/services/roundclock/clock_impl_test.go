package roundclock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/timesource"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/roundclock"
	chaintest "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
)

// stubTimeSource is a minimal in-test TimeSource. SubscribeRounds returns
// the channel that the test pushes into directly.
type stubTimeSource struct {
	rounds chan chain.Round
}

func newStub() *stubTimeSource {
	return &stubTimeSource{rounds: make(chan chain.Round, 16)}
}

func (s *stubTimeSource) CurrentRound(_ context.Context) (chain.Round, error) {
	return chain.Round{}, nil
}
func (s *stubTimeSource) CurrentL1Block(_ context.Context) (chain.BlockNumber, error) {
	return 0, nil
}
func (s *stubTimeSource) SubscribeRounds(_ context.Context) (<-chan chain.Round, error) {
	return s.rounds, nil
}
func (s *stubTimeSource) SubscribeL1Blocks(_ context.Context) (<-chan chain.BlockNumber, error) {
	return make(chan chain.BlockNumber), nil
}

// Compile-time: stubTimeSource satisfies timesource.TimeSource.
var _ timesource.TimeSource = (*stubTimeSource)(nil)

func TestNew_RequiresTimeSource(t *testing.T) {
	if _, err := roundclock.New(roundclock.Options{}); err == nil {
		t.Errorf("New without TimeSource should fail")
	}
}

func TestSubscribeRounds_Passthrough(t *testing.T) {
	ts := newStub()
	c, err := roundclock.New(roundclock.Options{TimeSource: ts})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := c.SubscribeRounds(context.Background())
	if err != nil {
		t.Fatalf("SubscribeRounds: %v", err)
	}
	ts.rounds <- chain.Round{Number: 7}
	select {
	case r := <-out:
		if r.Number != 7 {
			t.Errorf("got %d, want 7", r.Number)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive round")
	}
}

func TestSubscribeRoundsForName_DedupAcrossRestart(t *testing.T) {
	ts := newStub()
	st := chaintest.NewFakeStore()
	c, err := roundclock.New(roundclock.Options{TimeSource: ts, Store: st})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	nc := c.(roundclock.NamedClock)

	// First run: subscribe + ack rounds 1, 2, 3.
	ctx, cancel := context.WithCancel(context.Background())
	out1, err := nc.SubscribeRoundsForName(ctx, "test")
	if err != nil {
		t.Fatalf("SubscribeRoundsForName 1: %v", err)
	}

	for _, n := range []chain.RoundNumber{1, 2, 3} {
		ts.rounds <- chain.Round{Number: n}
		<-out1
		_ = nc.AckRound("test", n)
	}
	cancel()

	last, _ := nc.LastEmitted("test")
	if last != 3 {
		t.Errorf("LastEmitted = %d, want 3", last)
	}

	// Second run: re-subscribe under same name. Replay rounds 2, 3, 4 — only
	// 4 should arrive; 2 and 3 should be suppressed.
	ts2 := newStub()
	c2, _ := roundclock.New(roundclock.Options{TimeSource: ts2, Store: st})
	nc2 := c2.(roundclock.NamedClock)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	out2, err := nc2.SubscribeRoundsForName(ctx2, "test")
	if err != nil {
		t.Fatalf("SubscribeRoundsForName 2: %v", err)
	}

	go func() {
		ts2.rounds <- chain.Round{Number: 2}
		ts2.rounds <- chain.Round{Number: 3}
		ts2.rounds <- chain.Round{Number: 4}
	}()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case r := <-out2:
			if r.Number == 4 {
				return // success
			}
			if r.Number < 4 {
				t.Errorf("received suppressed round %d", r.Number)
			}
		case <-deadline:
			t.Fatal("did not receive round 4")
		}
	}
}

func TestSubscribeRoundsForName_RequiresName(t *testing.T) {
	ts := newStub()
	c, _ := roundclock.New(roundclock.Options{TimeSource: ts, Store: chaintest.NewFakeStore()})
	nc := c.(roundclock.NamedClock)
	if _, err := nc.SubscribeRoundsForName(context.Background(), ""); err == nil {
		t.Errorf("SubscribeRoundsForName(\"\") should fail")
	}
}

func TestAckRound_RequiresStore(t *testing.T) {
	ts := newStub()
	c, _ := roundclock.New(roundclock.Options{TimeSource: ts}) // no store
	nc := c.(roundclock.NamedClock)
	if err := nc.AckRound("test", 1); err == nil {
		t.Errorf("AckRound without store should fail")
	}
}

func TestAckRound_RequiresName(t *testing.T) {
	ts := newStub()
	c, _ := roundclock.New(roundclock.Options{TimeSource: ts, Store: chaintest.NewFakeStore()})
	nc := c.(roundclock.NamedClock)
	if err := nc.AckRound("", 1); err == nil {
		t.Errorf("AckRound(\"\") should fail")
	}
}

func TestLastEmitted_NewName(t *testing.T) {
	ts := newStub()
	c, _ := roundclock.New(roundclock.Options{TimeSource: ts, Store: chaintest.NewFakeStore()})
	nc := c.(roundclock.NamedClock)
	got, err := nc.LastEmitted("never-acked")
	if err != nil {
		t.Errorf("LastEmitted: %v", err)
	}
	if got != 0 {
		t.Errorf("LastEmitted = %d, want 0", got)
	}
}

func TestLastEmitted_NoStore(t *testing.T) {
	ts := newStub()
	c, _ := roundclock.New(roundclock.Options{TimeSource: ts})
	nc := c.(roundclock.NamedClock)
	got, err := nc.LastEmitted("any")
	if err != nil {
		t.Errorf("LastEmitted with no store: %v", err)
	}
	if got != 0 {
		t.Errorf("LastEmitted = %d, want 0", got)
	}
}

// Sanity that errors.Is is wired (silences unused-import on errors).
func TestErrors_Sanity(_ *testing.T) {
	_ = errors.Is(errors.New("a"), errors.New("a"))
}
