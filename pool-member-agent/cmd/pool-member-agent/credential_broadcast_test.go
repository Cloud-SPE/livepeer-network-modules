package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-member-agent/internal/attach"
)

func TestCredentialAndDesiredStateWakeEveryRegionalBroker(t *testing.T) {
	state := newRunnerState()
	one, two, three := state.wake(), state.wake(), state.wake()
	state.setCredential("new-credential")
	for _, wake := range []<-chan struct{}{one, two, three} {
		select {
		case <-wake:
		default:
			t.Fatal("credential did not broadcast")
		}
	}
	next := state.wake()
	select {
	case <-next:
		t.Fatal("new subscription is already closed")
	default:
	}
	state.set(nil, "draining")
	select {
	case <-next:
	default:
		t.Fatal("desired state did not broadcast")
	}
}
func TestEveryRefreshLoopUsesCurrentAttachCredential(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	state := newRunnerState()
	state.setCredential("old")
	cfg := config{HostID: "fleet-host", Credential: "old", RefreshEvery: time.Hour}
	doc, err := buildDocument(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan int, 3)
	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sent := false
			refreshLoop(ctx, cfg, state, doc, func(message tunnelMessage) error {
				var current attach.Document
				if err := json.Unmarshal(message.Body, &current); err != nil {
					return err
				}
				if current.Credential.Token == "new" && !sent {
					sent = true
					received <- i
				}
				return nil
			})
		}(i)
	}
	state.setCredential("new")
	for range 3 {
		select {
		case <-received:
		case <-ctx.Done():
			t.Fatal("not every broker refreshed")
		}
	}
	cancel()
	wg.Wait()
}
