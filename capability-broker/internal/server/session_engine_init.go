package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionengine"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
)

// initSessionEngine constructs the paid-session engine when any
// capability declares the protocol. Config validation has already
// required session_store and external_base_url in that case.
func (s *Server) initSessionEngine() error {
	if !s.servesProtocol("paid-session/") {
		return nil
	}
	key, err := sessionstore.LoadKeyFile(s.cfg.SessionStore.SealingKeyFile)
	if err != nil {
		return err
	}
	store, err := sessionstore.Open(s.cfg.SessionStore.Path, key)
	if err != nil {
		return err
	}
	s.sessionWS = newSessionWSHub()
	engine, err := sessionengine.New(sessionengine.Config{
		Store:   store,
		Payment: s.payment,
		Runner:  s.runnerClientFor,
		OnEvent: s.onEngineEvent,
		Specs: func(sessionID string) *sessionengine.OfferingSpec {
			rec, err := store.Get(sessionID)
			if err != nil {
				return nil
			}
			return s.specForRecord(rec)
		},
		Callback:      sessionengine.CallbackConfig{BaseURL: s.cfg.ExternalBaseURL},
		AllocDebitSeq: s.allocDebitSeq,
		OnWinddown:    observability.RecordSessionWinddown,
	})
	if err != nil {
		_ = store.Close()
		return fmt.Errorf("construct: %w", err)
	}
	s.sessionStore = store
	s.sessionEngine = engine
	return nil
}

// checkRunnerDescriptions reads each paid-session runner's own
// declaration and compares it against the offering configured against
// it (paid-session/v1 §7.1.1). A contradiction means the configuration
// is already broken — sessions would fail at open, or every usage event
// would be rejected — so the broker refuses to start rather than
// advertise something that cannot work. A runner that cannot be reached
// is only a warning: unreachability is not a contradiction.
//
// Declarations are never adopted. Published offerings are cold-key
// signed, so absorbing a runner's values would let a runner-side change
// alter what the orchestrator sells.
// applyRunnerDescriptions reads each paid-session runner's declaration
// and reconciles it with the configured tuple. It runs on the config
// BEFORE the server is built, so a readiness probe it derives is in
// place when the health manager is constructed, and reload takes the
// same path.
//
// Returns the tuples to quarantine (see checkRunnerDescriptions' doc).
// It mutates only cfg.Capabilities[i].Health.Probe, and only when the
// operator configured none.
func applyRunnerDescriptions(cfg *config.Config) map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	quarantined := map[string]string{}
	for i := range cfg.Capabilities {
		c := &cfg.Capabilities[i]
		if c.Session == nil || c.Session.Runner.DescribePath == "" {
			continue
		}
		client := &sessionengine.HTTPRunnerClient{
			BaseURL:   c.Backend.URL,
			AuthToken: bearerFromAuth(c.Backend.Auth),
		}
		desc, err := client.Describe(ctx, c.Session.Runner.DescribePath)
		if err != nil {
			log.Printf("warning: %s/%s: runner describe failed: %v (continuing; unreachability is not a contradiction)",
				c.ID, c.OfferingID, err)
			continue
		}
		if desc == nil {
			continue
		}
		var fatal []string
		for _, d := range sessionengine.CompareDescription(specFromCapability(c), desc) {
			if d.Fatal {
				fatal = append(fatal, d.String())
			} else {
				log.Printf("warning: %s", d.String())
			}
		}
		// Readiness is live data, not manifest data — deriving a probe
		// from it changes how liveness is measured, never what the orch
		// advertises and sells, so the never-adopt rule does not apply.
		// An operator-configured probe still wins.
		if match := describedFor(desc, c.ID); match != nil && match.Readiness != nil && match.Readiness.Path != "" {
			readyURL := strings.TrimRight(c.Backend.URL, "/") + match.Readiness.Path
			if isDefaultedProbe(c) {
				// The default probe hits the backend root and calls any
				// 200 healthy. The runner's own endpoint knows what
				// ready means for it, so prefer it.
				c.Health.Probe.Config["url"] = readyURL
				log.Printf("%s/%s: readiness probe pointed at the runner's declared endpoint (%s)",
					c.ID, c.OfferingID, readyURL)
			} else {
				log.Printf("%s/%s: runner declares readiness at %s but an operator probe (%s) is configured; operator wins",
					c.ID, c.OfferingID, readyURL, c.Health.Probe.Type)
			}
		}

		if match := describedFor(desc, c.ID); match != nil && len(match.SessionParamsSchema) > 0 {
			// Carried to gateways through the offering; never enforced.
			c.Session.SessionParamsSchema = match.SessionParamsSchema
		}

		if len(fatal) > 0 {
			// Fatal to THIS capability, not to the broker: one broken
			// tuple must not take down every other capability the
			// operator serves.
			reason := strings.Join(fatal, "; ")
			quarantined[c.ID+"|"+c.OfferingID] = reason
			log.Printf("ERROR: %s/%s quarantined — not served, not advertised: %s",
				c.ID, c.OfferingID, reason)
		}
	}
	return quarantined
}

// bearerFromAuth resolves an env:// bearer reference for broker→runner
// calls made before the Server exists.
func bearerFromAuth(auth config.AuthConfig) string {
	if auth.Method != "bearer" {
		return ""
	}
	if v, ok := strings.CutPrefix(auth.SecretRef, "env://"); ok {
		return os.Getenv(v)
	}
	return ""
}

// isDefaultedProbe reports whether a capability's probe is the one
// config validation fills in (http-status against the backend root)
// rather than something the operator wrote. An operator's own probe is
// never overridden.
func isDefaultedProbe(c *config.Capability) bool {
	if c.Health.Probe.Type != "http-status" || c.Health.Probe.Config == nil {
		return false
	}
	if _, hasPath := c.Health.Probe.Config["path"]; hasPath {
		return false
	}
	u, _ := c.Health.Probe.Config["url"].(string)
	return u == c.Backend.URL
}

// describedFor finds the described capability matching an id.
func describedFor(desc *sessionengine.RunnerDescription, capID string) *sessionengine.DescribedCapability {
	if desc == nil {
		return nil
	}
	for i := range desc.Capabilities {
		if desc.Capabilities[i].CapabilityID == capID {
			return &desc.Capabilities[i]
		}
	}
	return nil
}

// isQuarantined reports whether a capability tuple is withheld.
func (s *Server) isQuarantined(capID, offID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, bad := s.quarantined[capID+"|"+offID]
	return bad
}

// quarantineReasons returns a copy for operator surfaces.
func (s *Server) quarantineReasons() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.quarantined))
	for k, v := range s.quarantined {
		out[k] = v
	}
	return out
}
