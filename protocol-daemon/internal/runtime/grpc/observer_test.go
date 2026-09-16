package grpc

import (
	"context"
	"errors"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/types"
)

func TestObserverRoundReadsAndMutations(t *testing.T) {
	ctx := context.Background()
	rc := &stubRoundClockSrc{cur: chain.Round{Number: 103, Initialized: true}, rounds: make(chan chain.Round, 1)}
	srv, err := New(Config{Mode: types.ModeReadOnly, RC: rc})
	if err != nil {
		t.Fatal(err)
	}
	st, err := srv.GetRoundStatus(ctx, struct{}{})
	if err != nil || st.LastRound != 103 || !st.CurrentRoundInitialized {
		t.Fatalf("status %+v: %v", st, err)
	}
	rc.curErr = errors.New("RPC unavailable")
	if _, err := srv.GetRoundStatus(ctx, struct{}{}); err == nil {
		t.Fatal("returned stale status after RPC failure")
	}
	rc.rounds <- chain.Round{Number: 104}
	close(rc.rounds)
	var events []RoundEvent
	if err := srv.StreamRoundEvents(ctx, func(e RoundEvent) error { events = append(events, e); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Number != 104 {
		t.Fatalf("events: %+v", events)
	}
	checks := map[string]func() error{
		"initialize":  func() error { _, e := srv.ForceInitializeRound(ctx, struct{}{}); return e },
		"reward":      func() error { _, e := srv.ForceRewardCall(ctx, struct{}{}); return e },
		"config":      func() error { _, e := srv.SetConfig(ctx, types.OperationalConfig{}); return e },
		"transcoder":  func() error { _, e := srv.SetTranscoder(ctx, SetTranscoderRequest{}); return e },
		"transfer":    func() error { _, e := srv.ForceTransferBond(ctx, struct{}{}); return e },
		"withdraw":    func() error { _, e := srv.ForceWithdrawFees(ctx, struct{}{}); return e },
		"vote":        func() error { _, e := srv.CastVote(ctx, CastVoteRequest{}); return e },
		"registry":    func() error { _, e := srv.SetServiceURI(ctx, SetServiceURIRequest{}); return e },
		"ai_registry": func() error { _, e := srv.SetAIServiceURI(ctx, SetAIServiceURIRequest{}); return e },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(check(), ErrUnimplemented) {
				t.Fatal("observer mutation not rejected")
			}
		})
	}
	if _, err := New(Config{Mode: types.ModeReadOnly, RC: rc, Tx: &stubSubmitter{}}); err == nil {
		t.Fatal("observer accepted transaction dependency")
	}
}
