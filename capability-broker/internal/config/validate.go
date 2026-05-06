package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var (
	interactionModeRE = regexp.MustCompile(`^[a-z][a-z0-9-]*@v[0-9]+$`)
	ethAddressRE      = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	priceWeiRE        = regexp.MustCompile(`^[0-9]+$`)
)

// Validate runs cross-field validation against a parsed Config. Defaults are
// filled in for omitted-but-optional fields (e.g., Listen addresses).
func (c *Config) Validate() error {
	if !ethAddressRE.MatchString(c.Identity.OrchEthAddress) {
		return fmt.Errorf("identity.orch_eth_address: must be 0x-prefixed 40-hex (got %q)", c.Identity.OrchEthAddress)
	}

	if c.Listen.Paid == "" {
		c.Listen.Paid = ":8080"
	}
	if c.Listen.Metrics == "" {
		c.Listen.Metrics = ":9090"
	}

	if len(c.Capabilities) == 0 {
		return fmt.Errorf("capabilities: must declare at least one")
	}

	seen := make(map[string]struct{}, len(c.Capabilities))
	for i := range c.Capabilities {
		cap := &c.Capabilities[i]
		ctx := fmt.Sprintf("capabilities[%d]", i)
		if cap.ID != "" || cap.OfferingID != "" {
			ctx = fmt.Sprintf("capabilities[%d] (%s/%s)", i, cap.ID, cap.OfferingID)
		}

		if cap.ID == "" {
			return fmt.Errorf("%s: id is required", ctx)
		}
		if cap.OfferingID == "" {
			return fmt.Errorf("%s: offering_id is required", ctx)
		}
		key := cap.ID + "|" + cap.OfferingID
		if _, dup := seen[key]; dup {
			return fmt.Errorf("%s: duplicate (capability_id, offering_id) pair", ctx)
		}
		seen[key] = struct{}{}

		if !interactionModeRE.MatchString(cap.InteractionMode) {
			return fmt.Errorf("%s: interaction_mode must match <name>@v<major> (got %q)", ctx, cap.InteractionMode)
		}

		if cap.WorkUnit.Name == "" {
			return fmt.Errorf("%s: work_unit.name is required", ctx)
		}
		if len(cap.WorkUnit.Extractor) == 0 {
			return fmt.Errorf("%s: work_unit.extractor is required", ctx)
		}
		if _, ok := cap.WorkUnit.Extractor["type"].(string); !ok {
			return fmt.Errorf("%s: work_unit.extractor.type must be a string", ctx)
		}

		if !priceWeiRE.MatchString(cap.Price.AmountWei) {
			return fmt.Errorf("%s: price.amount_wei must be a non-negative decimal string (got %q)", ctx, cap.Price.AmountWei)
		}
		if cap.Price.PerUnits == 0 {
			return fmt.Errorf("%s: price.per_units must be > 0", ctx)
		}

		if cap.Backend.Transport == "" {
			return fmt.Errorf("%s: backend.transport is required", ctx)
		}
		switch cap.Backend.Transport {
		case "http":
			if cap.Backend.URL == "" {
				return fmt.Errorf("%s: backend.url is required for transport=http", ctx)
			}
			u, err := url.Parse(cap.Backend.URL)
			if err != nil {
				return fmt.Errorf("%s: backend.url is invalid: %w", ctx, err)
			}
			if u.Scheme != "http" && u.Scheme != "https" {
				return fmt.Errorf("%s: backend.url scheme must be http or https (got %q)", ctx, u.Scheme)
			}
		default:
			return fmt.Errorf("%s: backend.transport %q is not yet supported (only 'http' in v0.1)", ctx, cap.Backend.Transport)
		}

		switch cap.Backend.Auth.Method {
		case "", "none":
			// OK; "none" or unset => no auth.
		case "bearer":
			if cap.Backend.Auth.SecretRef == "" {
				return fmt.Errorf("%s: backend.auth.secret_ref is required when method=bearer", ctx)
			}
			if !strings.Contains(cap.Backend.Auth.SecretRef, "://") {
				return fmt.Errorf("%s: backend.auth.secret_ref should be a URI-style reference (got %q)", ctx, cap.Backend.Auth.SecretRef)
			}
		default:
			return fmt.Errorf("%s: backend.auth.method %q is not supported", ctx, cap.Backend.Auth.Method)
		}
	}

	return nil
}
