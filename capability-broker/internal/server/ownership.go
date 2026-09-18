package server

import (
	"context"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
	api "github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
)

type ownershipCacheKey struct{ host, device string }

// Ownership gates capability activation, not hardware-only enrollment. Reject
// only the affected capabilities so transferring one GPU leaves siblings live.
func (s *Server) checkAttachOwnership(doc *runnerattach.Document, res *runnerattach.Result) {
	cfg := s.currentConfig()
	if cfg.Ownership.URL == "" {
		return
	}
	s.ownershipMu.Lock()
	defer s.ownershipMu.Unlock()
	if s.ownershipChecks == nil {
		s.ownershipChecks = map[ownershipCacheKey]uint64{}
	}
	clientCfg := cfg.Ownership
	clientCfg.PoolID = cfg.PoolID
	client, clientErr := api.NewClient(clientCfg)
	for i := range res.Capabilities {
		result := &res.Capabilities[i]
		if result.Status != "accepted" {
			continue
		}
		cap := doc.Capabilities[result.Index]
		valid := s.credentialStore != nil && len(cap.Devices) > 0 && clientErr == nil
		if valid {
			credential, err := s.credentialStore.ByHost(doc.HostID)
			valid = err == nil && credential.PoolID == cfg.PoolID
			if valid {
				for _, device := range cap.Devices {
					device = strings.ToLower(device)
					generation := credential.DeviceOwnership[device]
					key := ownershipCacheKey{doc.HostID, device}
					if generation == 0 {
						delete(s.ownershipChecks, key)
						valid = false
						break
					}
					if s.ownershipChecks[key] == generation {
						continue
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					record, err := client.Get(ctx, device)
					cancel()
					if err != nil || record.State != "active" || record.PoolID != cfg.PoolID || record.EnrollmentID != doc.HostID || record.Generation != generation {
						valid = false
						break
					}
					s.ownershipChecks[key] = generation
				}
			}
		}
		if !valid {
			result.Status = "rejected"
			result.Reasons = append(result.Reasons, runnerattach.Reason{Code: "ownership_fenced", Field: "devices", Message: "regional device ownership could not be validated"})
		}
	}
}

func (s *Server) pruneOwnershipChecks() {
	s.ownershipMu.Lock()
	defer s.ownershipMu.Unlock()
	for key, generation := range s.ownershipChecks {
		record, err := s.credentialStore.ByHost(key.host)
		if err != nil || record.DeviceOwnership[key.device] != generation {
			delete(s.ownershipChecks, key)
		}
	}
}

// Dispatch uses only grants validated in this process and the latest durable
// credential snapshot. Authority loss does not halt existing execution; removed
// device grants immediately prevent new work even from stale selections.
func (s *Server) authorizeDeviceDispatch(host string, capability runnerattach.Capability) bool {
	cfg := s.currentConfig()
	if s.workAccounting != nil && s.workAccounting.RequireTerms {
		if s.credentialStore == nil || s.workAccounting.Store.TermsPermitAdmission() != nil {
			return false
		}
		policy, err := s.workAccounting.Store.TermsPolicy()
		if err != nil {
			return false
		}
		credential, err := s.credentialStore.ByHost(host)
		if err != nil || credential.TermsVersion != policy.Version || credential.PoolID != cfg.PoolID {
			return false
		}
	}

	if cfg.Ownership.URL == "" {
		return true
	}
	if s.credentialStore == nil || len(capability.Devices) == 0 {
		return false
	}
	s.ownershipMu.Lock()
	defer s.ownershipMu.Unlock()
	credential, err := s.credentialStore.ByHost(host)
	if err != nil || credential.PoolID != cfg.PoolID {
		return false
	}
	for _, device := range capability.Devices {
		device = strings.ToLower(device)
		generation := credential.DeviceOwnership[device]
		if s.workAccounting != nil {
			draining, err := s.workAccounting.Store.DeviceDraining(host, device, generation)
			if err != nil || draining {
				return false
			}
		}
		if generation == 0 || s.ownershipChecks[ownershipCacheKey{host, device}] != generation {
			return false
		}
	}
	return true
}
