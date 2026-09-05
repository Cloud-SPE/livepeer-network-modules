package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var (
	ethAddressRE = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	priceWeiRE   = regexp.MustCompile(`^[0-9]+$`)
)

var deprecatedOpenAICapabilityIDSuffixes = []string{
	"openai:chat-completions:",
	"openai:embeddings:",
	"openai:audio-transcriptions:",
	"openai:audio-speech:",
	"openai:images-generations:",
	"openai:realtime:",
}

var audioTaskByCapabilityID = map[string]string{
	"openai:audio-transcriptions": "transcription",
	"openai:audio-speech":         "speech",
}

var videoTaskByCapabilityID = map[string]string{
	"video:transcode.vod": "transcode",
	"video:transcode.abr": "abr-transcode",
}

var vtuberTaskByCapabilityID = map[string]string{
	"livepeer:vtuber-session": "session",
}

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
	switch c.AdminAuth.Method {
	case "", "none":
	case "bearer":
		if c.AdminAuth.SecretRef == "" {
			return fmt.Errorf("admin_auth.secret_ref is required when admin_auth.method=bearer")
		}
		if !strings.HasPrefix(c.AdminAuth.SecretRef, "env://") {
			return fmt.Errorf("admin_auth.secret_ref must use env:// (got %q)", c.AdminAuth.SecretRef)
		}
	default:
		return fmt.Errorf("admin_auth.method %q is not supported", c.AdminAuth.Method)
	}
	if (c.SessionStore.Path == "") != (c.SessionStore.SealingKeyFile == "") {
		return fmt.Errorf("session_store: path and sealing_key_file must be set together")
	}
	if (c.CredentialStore.Path == "") != (c.CredentialStore.SealingKeyFile == "") {
		return fmt.Errorf("credential_store: path and sealing_key_file must be set together")
	}
	if c.CredentialStore.DefaultExpirySeconds < 0 || c.CredentialStore.MaxExpirySeconds < 0 {
		return fmt.Errorf("credential_store: expiry seconds must be >= 0")
	}
	if c.CredentialStore.MaxExpirySeconds > 0 && c.CredentialStore.DefaultExpirySeconds > c.CredentialStore.MaxExpirySeconds {
		return fmt.Errorf("credential_store: default_expiry_seconds exceeds max_expiry_seconds")
	}
	if c.PoolSnapshot.URL != "" {
		u, err := url.Parse(c.PoolSnapshot.URL)
		if err != nil {
			return fmt.Errorf("pool_snapshot.url is invalid: %w", err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("pool_snapshot.url scheme must be http or https (got %q)", u.Scheme)
		}
		switch c.PoolSnapshot.Auth.Method {
		case "", "none":
		case "bearer":
			if c.PoolSnapshot.Auth.SecretRef == "" {
				return fmt.Errorf("pool_snapshot.auth.secret_ref is required when method=bearer")
			}
			if !strings.Contains(c.PoolSnapshot.Auth.SecretRef, "://") {
				return fmt.Errorf("pool_snapshot.auth.secret_ref should be a URI-style reference (got %q)", c.PoolSnapshot.Auth.SecretRef)
			}
		default:
			return fmt.Errorf("pool_snapshot.auth.method %q is not supported", c.PoolSnapshot.Auth.Method)
		}
		if c.PoolSnapshot.TimeoutMS < 0 {
			return fmt.Errorf("pool_snapshot.timeout_ms must be >= 0")
		}
		if c.PoolSnapshot.PollIntervalMS < 0 {
			return fmt.Errorf("pool_snapshot.poll_interval_ms must be >= 0")
		}
		if c.PoolSnapshot.StaleAfterMS < 0 {
			return fmt.Errorf("pool_snapshot.stale_after_ms must be >= 0")
		}
		if c.PoolSnapshot.ExpireAfterMS < 0 {
			return fmt.Errorf("pool_snapshot.expire_after_ms must be >= 0")
		}
		if c.PoolSnapshot.TimeoutMS == 0 {
			c.PoolSnapshot.TimeoutMS = 1500
		}
		if c.PoolSnapshot.PollIntervalMS == 0 {
			c.PoolSnapshot.PollIntervalMS = 5000
		}
		if c.PoolSnapshot.StaleAfterMS == 0 {
			c.PoolSnapshot.StaleAfterMS = 15000
		}
		if c.PoolSnapshot.ExpireAfterMS == 0 {
			c.PoolSnapshot.ExpireAfterMS = 60000
		}
		if c.PoolSnapshot.ExpireAfterMS <= c.PoolSnapshot.StaleAfterMS {
			return fmt.Errorf("pool_snapshot.expire_after_ms must be greater than pool_snapshot.stale_after_ms")
		}
	} else if c.PoolSnapshot.Auth.Method != "" || c.PoolSnapshot.Auth.SecretRef != "" ||
		c.PoolSnapshot.TimeoutMS != 0 || c.PoolSnapshot.PollIntervalMS != 0 ||
		c.PoolSnapshot.StaleAfterMS != 0 || c.PoolSnapshot.ExpireAfterMS != 0 {
		return fmt.Errorf("pool_snapshot.url is required when pool_snapshot is configured")
	}
	if c.ReceiptSink.URL != "" {
		u, err := url.Parse(c.ReceiptSink.URL)
		if err != nil {
			return fmt.Errorf("receipt_sink.url is invalid: %w", err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("receipt_sink.url scheme must be http or https (got %q)", u.Scheme)
		}
		switch c.ReceiptSink.Auth.Method {
		case "", "none":
		case "bearer":
			if c.ReceiptSink.Auth.SecretRef == "" {
				return fmt.Errorf("receipt_sink.auth.secret_ref is required when method=bearer")
			}
			if !strings.Contains(c.ReceiptSink.Auth.SecretRef, "://") {
				return fmt.Errorf("receipt_sink.auth.secret_ref should be a URI-style reference (got %q)", c.ReceiptSink.Auth.SecretRef)
			}
		default:
			return fmt.Errorf("receipt_sink.auth.method %q is not supported", c.ReceiptSink.Auth.Method)
		}
		if c.ReceiptSink.TimeoutMS < 0 {
			return fmt.Errorf("receipt_sink.timeout_ms must be >= 0")
		}
	}

	if err := c.validateOffers(); err != nil {
		return err
	}
	if len(c.Offers) == 0 && c.OffersSource != OffersSourceAdmin {
		return fmt.Errorf("offers: must declare at least one (or set offers_source: admin)")
	}

	return nil
}

func validateExtraGrammar(ctx, capabilityID string, extra map[string]any) error {
	if raw, ok := extra["openai"]; ok {
		if _, ok := raw.(map[string]any); !ok {
			return fmt.Errorf("%s: extra.openai must be a map for %s", ctx, capabilityID)
		}
	}
	if raw, ok := extra["features"]; ok {
		features, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: extra.features must be a map for %s", ctx, capabilityID)
		}
		for key, value := range features {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("%s: extra.features.%s must be a boolean for %s", ctx, key, capabilityID)
			}
		}
	}
	for key, byCapability := range map[string]map[string]string{
		"audio":  audioTaskByCapabilityID,
		"video":  videoTaskByCapabilityID,
		"vtuber": vtuberTaskByCapabilityID,
	} {
		raw, present := extra[key]
		if !present {
			continue
		}
		nested, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: extra.%s must be a map for %s", ctx, key, capabilityID)
		}
		task := strings.TrimSpace(asString(nested["task"]))
		want, known := byCapability[capabilityID]
		if task != "" && known && task != want {
			return fmt.Errorf("%s: extra.%s.task %q is invalid for %s; want %q", ctx, key, task, capabilityID, want)
		}
	}
	return nil
}

// validateCapabilityID rejects capability ids the manifest no longer
// admits. The model used to be encoded in the id itself
// ("openai:chat-completions:llama-3-70b"); it is now a runner-declared
// identity fact that an offer selects on, so the suffixed form would
// advertise a capability no gateway resolves.
func validateCapabilityID(ctx, capabilityID string) error {
	for _, prefix := range deprecatedOpenAICapabilityIDSuffixes {
		if strings.HasPrefix(capabilityID, prefix) {
			return fmt.Errorf("%s: capability %q uses the deprecated model-in-id syntax; use %q and select the model with match: {identity.openai.model: ...}",
				ctx, capabilityID, strings.TrimSuffix(prefix, ":"))
		}
	}
	return nil
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
