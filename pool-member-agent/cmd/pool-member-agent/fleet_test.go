package main

import (
	"context"
	"testing"
	"time"
)

func TestBrokerFleetOrigins(t *testing.T) {
	for _, raw := range []string{"http://broker", "https://a,", "https://a,https://A", "https://a/path", "https://user@a", "https://a?", "https://a#x"} {
		if _, err := parseBrokerFleet(raw, ""); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	if _, err := parseBrokerFleet("https://a", "a:8443"); err == nil {
		t.Fatal("accepted ambiguous transport")
	}
	urls, err := parseBrokerFleet("https://transcode, https://audio/,https://llm", "")
	if err != nil || len(urls) != 3 || urls[1] != "https://audio" {
		t.Fatalf("%v %v", urls, err)
	}
}

func TestFleetOfflineBrokerDoesNotBlockHealthyConnections(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	state := initialRunnerState(config{Credential: "original"})
	connected := make(chan string, 3)
	finished := make(chan error, 1)
	go func() {
		finished <- tunnelFleet(ctx, config{BrokerURLs: []string{"https://offline", "https://audio", "https://llm"}}, state, func(ctx context.Context, c config, shared *runnerState) error {
			if shared != state || len(c.BrokerURLs) != 0 || c.BrokerQUICAddr != "" {
				t.Error("fleet lost shared state or isolated config")
			}
			if c.BrokerURL != "https://offline" {
				connected <- c.BrokerURL
			}
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	for range 2 {
		select {
		case <-connected:
		case <-time.After(time.Second):
			t.Fatal("offline broker blocked healthy attachment")
		}
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("fleet did not join all loops")
	}
}
