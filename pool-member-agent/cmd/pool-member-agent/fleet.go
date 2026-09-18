package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

func parseBrokerFleet(raw, quic string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if quic != "" {
		return nil, fmt.Errorf("LIVEPEER_BROKER_URLS cannot be combined with LIVEPEER_BROKER_QUIC_ADDR")
	}
	seen := map[string]bool{}
	var out []string
	for _, item := range strings.Split(raw, ",") {
		origin := strings.TrimRight(strings.TrimSpace(item), "/")
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return nil, fmt.Errorf("LIVEPEER_BROKER_URLS requires distinct HTTPS origins")
		}
		key := strings.ToLower(u.Host)
		if seen[key] {
			return nil, fmt.Errorf("duplicate broker origin")
		}
		seen[key] = true
		out = append(out, origin)
	}
	return out, nil
}

// Each broker owns its reconnect/backoff loop; all share one desired-state
// applier and one enrollment's credential and GPU ownership generation.
func tunnelFleet(ctx context.Context, cfg config, state *runnerState, loop func(context.Context, config, *runnerState) error) error {
	if len(cfg.BrokerURLs) == 0 {
		return loop(ctx, cfg, state)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, len(cfg.BrokerURLs))
	for _, origin := range cfg.BrokerURLs {
		member := cfg
		member.BrokerURL, member.BrokerURLs, member.BrokerQUICAddr = origin, nil, ""
		wg.Add(1)
		go func() { defer wg.Done(); errs <- loop(ctx, member, state) }()
	}
	// Production loops return only on cancellation. An unexpected termination
	// stops the process so its supervisor can restart the complete fleet.
	err := <-errs
	cancel()
	wg.Wait()
	return err
}
