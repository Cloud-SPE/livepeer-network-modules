package main

import (
	"context"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/providers/roundsmanager"
	grpcrt "github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/runtime/grpc"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/runtime/lifecycle"
)

// runObserver has no transaction manager or operational configuration store.
// In particular, opening an existing database never resumes transaction intents.
func runObserver(ctx context.Context, cfg config.Config, deps *providerSet, log logger.Logger) int {
	var locks grpcrt.LockReader
	if !cfg.Dev {
		rm, err := roundsmanager.New(deps.RPC, deps.Controller.Addresses().RoundsManager)
		if err != nil {
			log.Error("observer rounds manager", logger.Err(err))
			return 1
		}
		locks = rm
		if _, err := deps.RoundClock.Current(ctx); err != nil {
			log.Error("observer initial round", logger.Err(err))
			return 1
		}
	}
	srv, err := grpcrt.New(grpcrt.Config{Mode: cfg.Mode, Version: cfg.Version, ChainID: uint64(cfg.Chain.ChainID), RC: deps.RoundClock, LockReader: locks})
	if err != nil {
		log.Error("observer server", logger.Err(err))
		return 1
	}
	lis, err := grpcrt.NewListener(grpcrt.ListenerConfig{SocketPath: cfg.SocketPath, Server: srv, Logger: log, Version: cfg.Version})
	if err != nil {
		log.Error("observer listener", logger.Err(err))
		return 1
	}
	if err := lifecycle.Run(ctx, lifecycle.Config{Mode: cfg.Mode, RoundClock: deps.RoundClock, Listener: lis, Logger: log}); err != nil {
		log.Error("observer lifecycle", logger.Err(err))
		return 1
	}
	return 0
}
