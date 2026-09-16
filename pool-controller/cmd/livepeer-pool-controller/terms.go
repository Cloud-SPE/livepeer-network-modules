package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/termspublication"
)

func regionalTermsService(state *runtimeState) *termspublication.Service {
	return &termspublication.Service{Repo: state.repo, Broker: func(source revenue.Source) (termspublication.Broker, error) {
		return regionalBrokerAdmin(state, source)
	}}
}
func regionalBrokerAdmin(state *runtimeState, source revenue.Source) (*brokeradmin.Client, error) {
	cfg, _, _ := state.Snapshot()
	if cfg == nil || cfg.ServiceAuthFile == "" {
		return nil, fmt.Errorf("regional scoped service access required")
	}
	for _, target := range cfg.Bootstrap.BrokerTargets() {
		if target.Name != source.BrokerID {
			continue
		}
		if strings.TrimRight(target.AdminURL, "/") != strings.TrimRight(source.URL, "/") || target.Auth.Method != "scoped" || target.Auth.PoolID != state.repo.PoolID() {
			return nil, fmt.Errorf("broker %s requires its registered origin and scoped regional controller credential", source.BrokerID)
		}
		timeout := time.Duration(target.TimeoutMS) * time.Millisecond
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		return brokeradmin.New(target.AdminURL, target.Auth, timeout), nil
	}
	return nil, fmt.Errorf("registered broker %s missing controller service access", source.BrokerID)
}
