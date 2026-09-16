package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/devicetransfer"
)

func regionalDeviceTransferService(state *runtimeState) (*devicetransfer.Service, error) {
	cfg, _, _ := state.Snapshot()
	if cfg == nil || cfg.ServiceAuthFile == "" || cfg.Ownership.URL == "" {
		return nil, fmt.Errorf("scoped regional ownership service required")
	}
	authorityCfg := cfg.Ownership
	authorityCfg.PoolID = state.repo.PoolID()
	authority, err := ownership.NewClient(authorityCfg)
	if err != nil {
		return nil, err
	}
	return &devicetransfer.Service{Repo: state.repo, Authority: authority, Broker: func(source revenue.Source) (devicetransfer.Broker, error) { return regionalBrokerAdmin(state, source) }}, nil
}
func runDeviceTransferLoop(ctx context.Context, state *runtimeState, stderr io.Writer) {
	cfg, _, _ := state.Snapshot()
	if cfg == nil || cfg.Ownership.URL == "" {
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		svc, err := regionalDeviceTransferService(state)
		if err == nil {
			err = svc.Resume(ctx)
		}
		if err != nil {
			fmt.Fprintf(stderr, "regional device transfer held: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
