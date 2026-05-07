package server

import (
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/sessionrunner"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes/sessioncontrolplusmedia"
)

// sessionRunnerResolver returns a CapabilityResolver the runner-backend
// uses to map a session id to the operator-declared image / command /
// env tuple. The mapping is one-shot: the session-control-plus-media
// driver records the session's capability at session-open; the
// resolver looks up the corresponding host-config entry and renders
// it as a sessionrunner.CapabilityBackend.
func sessionRunnerResolver(cfg *config.Config, store *sessioncontrolplusmedia.Store) sessioncontrolplusmedia.CapabilityResolver {
	return func(sessionID string) (sessionrunner.CapabilityBackend, bool) {
		rec := store.Get(sessionID)
		if rec == nil {
			return sessionrunner.CapabilityBackend{}, false
		}
		var cap *config.Capability
		for i := range cfg.Capabilities {
			c := &cfg.Capabilities[i]
			if c.ID == rec.CapabilityID && c.OfferingID == rec.OfferingID {
				cap = c
				break
			}
		}
		if cap == nil {
			return sessionrunner.CapabilityBackend{}, false
		}
		spec := cap.Backend.SessionRunner
		if spec == nil {
			return sessionrunner.CapabilityBackend{}, false
		}
		startup, _ := time.ParseDuration(spec.StartupTimeout)
		return sessionrunner.CapabilityBackend{
			Image:          spec.Image,
			Command:        spec.Command,
			Env:            spec.Env,
			MemoryLimit:    spec.Resources.Memory,
			CPULimit:       spec.Resources.CPU,
			GPUs:           spec.Resources.GPUs,
			NetworkMode:    spec.NetworkMode,
			StartupTimeout: startup,
		}, true
	}
}
