package config

import "fmt"

func applyLegacyDefaults(cfg *Config) {
	for i := range cfg.Members {
		if cfg.Members[i].PayoutMode == "" {
			cfg.Members[i].PayoutMode = "onchain"
		}
	}
}

func validateLegacyMembers(cfg *Config) error {
	if len(cfg.Members) == 0 {
		if cfg.Bootstrap.ImportLegacyConfigPath != "" || cfg.Bootstrap.AutoImportLegacyConfig {
			return nil
		}
		return nil
	}

	seenMembers := map[string]struct{}{}
	for memberIndex, member := range cfg.Members {
		if member.EthAddress == "" {
			return fmt.Errorf("members[%d].eth_address is required", memberIndex)
		}
		switch member.PayoutMode {
		case "", "onchain", "manual":
		default:
			return fmt.Errorf("members[%d].payout_mode must be one of onchain|manual", memberIndex)
		}
		if _, ok := seenMembers[member.EthAddress]; ok {
			return fmt.Errorf("duplicate member eth_address %q", member.EthAddress)
		}
		seenMembers[member.EthAddress] = struct{}{}
		if len(member.Backends) == 0 {
			return fmt.Errorf("members[%d].backends must contain at least one backend", memberIndex)
		}

		seenBackendIDs := map[string]struct{}{}
		for backendIndex, backend := range member.Backends {
			if backend.ID == "" {
				return fmt.Errorf("members[%d].backends[%d].id is required", memberIndex, backendIndex)
			}
			if _, ok := seenBackendIDs[backend.ID]; ok {
				return fmt.Errorf("duplicate backend id %q for member %q", backend.ID, member.EthAddress)
			}
			seenBackendIDs[backend.ID] = struct{}{}
			if backend.Transport == "" {
				return fmt.Errorf("members[%d].backends[%d].transport is required", memberIndex, backendIndex)
			}
			if backend.URL == "" && backend.Transport == "http" {
				return fmt.Errorf("members[%d].backends[%d].url is required for http transport", memberIndex, backendIndex)
			}
			switch backend.Auth.Method {
			case "", "none":
			case "bearer":
				if backend.Auth.SecretRef == "" {
					return fmt.Errorf("members[%d].backends[%d].auth.secret_ref is required when auth.method=bearer", memberIndex, backendIndex)
				}
			default:
				return fmt.Errorf("members[%d].backends[%d].auth.method %q is not supported", memberIndex, backendIndex, backend.Auth.Method)
			}
			if len(backend.Offerings) == 0 {
				return fmt.Errorf("members[%d].backends[%d].offerings must contain at least one offering", memberIndex, backendIndex)
			}

			for offeringIndex, offering := range backend.Offerings {
				if offering.CapabilityID == "" {
					return fmt.Errorf("members[%d].backends[%d].offerings[%d].capability_id is required", memberIndex, backendIndex, offeringIndex)
				}
				if offering.OfferingID == "" {
					return fmt.Errorf("members[%d].backends[%d].offerings[%d].offering_id is required", memberIndex, backendIndex, offeringIndex)
				}
				if offering.InteractionMode == "" {
					return fmt.Errorf("members[%d].backends[%d].offerings[%d].interaction_mode is required", memberIndex, backendIndex, offeringIndex)
				}
				if offering.WorkUnit.Name == "" {
					return fmt.Errorf("members[%d].backends[%d].offerings[%d].work_unit.name is required", memberIndex, backendIndex, offeringIndex)
				}
				if len(offering.WorkUnit.Extractor) == 0 {
					return fmt.Errorf("members[%d].backends[%d].offerings[%d].work_unit.extractor is required", memberIndex, backendIndex, offeringIndex)
				}
				if offering.Price.AmountWei == "" {
					return fmt.Errorf("members[%d].backends[%d].offerings[%d].price.amount_wei is required", memberIndex, backendIndex, offeringIndex)
				}
				if offering.Price.PerUnits == 0 {
					return fmt.Errorf("members[%d].backends[%d].offerings[%d].price.per_units must be > 0", memberIndex, backendIndex, offeringIndex)
				}
				if offering.CapabilityID == "video:live.rtmp" || offering.InteractionMode == "rtmp-ingress-hls-egress@v0" {
					return fmt.Errorf(
						"members[%d].backends[%d].offerings[%d] uses unsupported Pool live RTMP topology (%s / %s): pool-controller currently supports backend-provider-only members, but video:live.rtmp is broker-local ffmpeg-subprocess + RTMP/HLS; see docs/exec-plans/active/0032-pool-live-rtmp-contract-decision.md",
						memberIndex,
						backendIndex,
						offeringIndex,
						offering.CapabilityID,
						offering.InteractionMode,
					)
				}
			}
		}
	}

	return nil
}
