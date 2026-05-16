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

var validHealthStatuses = map[string]bool{
	"":            true,
	"ready":       true,
	"draining":    true,
	"degraded":    true,
	"unreachable": true,
	"stale":       true,
}

var validProbeTypes = map[string]bool{
	"":                        true,
	"http-status":             true,
	"http-jsonpath":           true,
	"http-openai-model-ready": true,
	"tcp-connect":             true,
	"command-exit-0":          true,
	"manual-drain":            true,
}

var validEncoderProfiles = map[string]bool{
	"passthrough":             true,
	"h264-live-1080p-libx264": true,
	"h264-live-1080p-nvenc":   true,
	"h264-live-1080p-qsv":     true,
	"h264-live-1080p-vaapi":   true,
}

var deprecatedOpenAICapabilityIDSuffixes = []string{
	"openai:chat-completions:",
	"openai:embeddings:",
	"openai:audio-transcriptions:",
	"openai:audio-speech:",
	"openai:images-generations:",
	"openai:realtime:",
}

var openAICapabilityIDsRequiringModel = map[string]struct{}{
	"openai:chat-completions":     {},
	"openai:embeddings":           {},
	"openai:audio-transcriptions": {},
	"openai:audio-speech":         {},
	"openai:images-generations":   {},
	"openai:realtime":             {},
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

func encoderProfileList() []string {
	out := make([]string, 0, len(validEncoderProfiles))
	for k := range validEncoderProfiles {
		out = append(out, k)
	}
	return out
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
		for _, prefix := range deprecatedOpenAICapabilityIDSuffixes {
			if strings.HasPrefix(cap.ID, prefix) {
				return fmt.Errorf("%s: id %q uses deprecated OpenAI capability syntax; use %q and set extra.openai.model instead",
					ctx, cap.ID, strings.TrimSuffix(prefix, ":"))
			}
		}
		if cap.OfferingID == "" {
			return fmt.Errorf("%s: offering_id is required", ctx)
		}
		if err := validateCapabilityExtra(ctx, cap); err != nil {
			return err
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
		case "ffmpeg-subprocess":
			if cap.Backend.Profile == "" {
				return fmt.Errorf("%s: backend.profile is required for transport=ffmpeg-subprocess", ctx)
			}
			if !validEncoderProfiles[cap.Backend.Profile] {
				return fmt.Errorf("%s: backend.profile %q is not one of %v", ctx, cap.Backend.Profile, encoderProfileList())
			}
		default:
			return fmt.Errorf("%s: backend.transport %q is not yet supported (only 'http' or 'ffmpeg-subprocess' in v0.1)", ctx, cap.Backend.Transport)
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

		if !validHealthStatuses[cap.Health.InitialStatus] {
			return fmt.Errorf("%s: health.initial_status %q is invalid", ctx, cap.Health.InitialStatus)
		}
		if cap.Health.InitialStatus == "" {
			cap.Health.InitialStatus = "stale"
		}

		switch {
		case cap.Health.Drain.Enabled:
			if cap.Health.Probe.Type == "" {
				cap.Health.Probe.Type = "manual-drain"
			}
		case cap.Health.Probe.Type == "":
			if cap.Backend.Transport == "http" && cap.Backend.URL != "" {
				cap.Health.Probe.Type = "http-status"
				if cap.Health.Probe.Config == nil {
					cap.Health.Probe.Config = map[string]any{}
				}
				if _, ok := cap.Health.Probe.Config["url"]; !ok {
					cap.Health.Probe.Config["url"] = cap.Backend.URL
				}
			}
		}

		if !validProbeTypes[cap.Health.Probe.Type] {
			return fmt.Errorf("%s: health.probe.type %q is invalid", ctx, cap.Health.Probe.Type)
		}
		if cap.Health.Probe.Type != "" && cap.Health.Probe.Type != "manual-drain" {
			if cap.Health.Probe.IntervalMS == 0 {
				cap.Health.Probe.IntervalMS = 5000
			}
			if cap.Health.Probe.TimeoutMS == 0 {
				cap.Health.Probe.TimeoutMS = 1500
			}
			if cap.Health.Probe.UnhealthyAfter == 0 {
				cap.Health.Probe.UnhealthyAfter = 2
			}
			if cap.Health.Probe.HealthyAfter == 0 {
				cap.Health.Probe.HealthyAfter = 1
			}
			if cap.Health.Probe.IntervalMS <= 0 {
				return fmt.Errorf("%s: health.probe.interval_ms must be > 0", ctx)
			}
			if cap.Health.Probe.TimeoutMS <= 0 {
				return fmt.Errorf("%s: health.probe.timeout_ms must be > 0", ctx)
			}
			if cap.Health.Probe.UnhealthyAfter < 1 {
				return fmt.Errorf("%s: health.probe.unhealthy_after must be >= 1", ctx)
			}
			if cap.Health.Probe.HealthyAfter < 1 {
				return fmt.Errorf("%s: health.probe.healthy_after must be >= 1", ctx)
			}
		}
		if cap.Health.Probe.Config == nil {
			cap.Health.Probe.Config = map[string]any{}
		}
		switch cap.Health.Probe.Type {
		case "http-status", "http-jsonpath", "http-openai-model-ready":
			if _, ok := cap.Health.Probe.Config["url"]; !ok && cap.Backend.URL != "" {
				cap.Health.Probe.Config["url"] = cap.Backend.URL
			}
			rawURL, _ := cap.Health.Probe.Config["url"].(string)
			if cap.Health.Probe.Type != "" && cap.Health.Probe.Type != "manual-drain" && rawURL == "" {
				return fmt.Errorf("%s: health.probe.config.url is required for %s", ctx, cap.Health.Probe.Type)
			}
			if cap.Health.Probe.Type == "http-jsonpath" {
				if _, ok := cap.Health.Probe.Config["path"].(string); !ok {
					return fmt.Errorf("%s: health.probe.config.path must be a string for http-jsonpath", ctx)
				}
			}
			if cap.Health.Probe.Type == "http-openai-model-ready" {
				if _, ok := cap.Health.Probe.Config["expect_model"].(string); !ok {
					return fmt.Errorf("%s: health.probe.config.expect_model must be a string for http-openai-model-ready", ctx)
				}
			}
		case "tcp-connect":
			if _, ok := cap.Health.Probe.Config["address"]; !ok && cap.Backend.URL != "" {
				if u, err := url.Parse(cap.Backend.URL); err == nil && u.Host != "" {
					cap.Health.Probe.Config["address"] = u.Host
				}
			}
			rawAddr, _ := cap.Health.Probe.Config["address"].(string)
			if rawAddr == "" {
				return fmt.Errorf("%s: health.probe.config.address is required for tcp-connect", ctx)
			}
		case "command-exit-0":
			cmd, ok := cap.Health.Probe.Config["command"].([]any)
			if !ok || len(cmd) == 0 {
				return fmt.Errorf("%s: health.probe.config.command must be a non-empty list for command-exit-0", ctx)
			}
		}
	}

	return nil
}

func validateCapabilityExtra(ctx string, cap *Capability) error {
	provider := strings.TrimSpace(asString(cap.Extra["provider"]))
	if strings.HasPrefix(cap.ID, "openai:") {
		openaiRaw, ok := cap.Extra["openai"]
		if !ok {
			return fmt.Errorf("%s: extra.openai is required for %s", ctx, cap.ID)
		}
		openaiExtra, ok := openaiRaw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: extra.openai must be a map for %s", ctx, cap.ID)
		}
		if provider == "" {
			return fmt.Errorf("%s: extra.provider is required for %s", ctx, cap.ID)
		}
		if _, needsModel := openAICapabilityIDsRequiringModel[cap.ID]; needsModel {
			model := strings.TrimSpace(asString(openaiExtra["model"]))
			if model == "" {
				return fmt.Errorf("%s: extra.openai.model is required for %s", ctx, cap.ID)
			}
		}
		if featuresRaw, ok := cap.Extra["features"]; ok {
			features, ok := featuresRaw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: extra.features must be a map for %s", ctx, cap.ID)
			}
			for key, value := range features {
				if _, ok := value.(bool); !ok {
					return fmt.Errorf("%s: extra.features.%s must be a boolean for %s", ctx, key, cap.ID)
				}
			}
		}
	}
	if requiredTask, ok := audioTaskByCapabilityID[cap.ID]; ok {
		if provider == "" {
			return fmt.Errorf("%s: extra.provider is required for %s", ctx, cap.ID)
		}
		audioRaw, ok := cap.Extra["audio"]
		if !ok {
			return fmt.Errorf("%s: extra.audio is required for %s", ctx, cap.ID)
		}
		audioExtra, ok := audioRaw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: extra.audio must be a map for %s", ctx, cap.ID)
		}
		task := strings.TrimSpace(asString(audioExtra["task"]))
		if task == "" {
			return fmt.Errorf("%s: extra.audio.task is required for %s", ctx, cap.ID)
		}
		if task != requiredTask {
			return fmt.Errorf("%s: extra.audio.task %q is invalid for %s; want %q", ctx, task, cap.ID, requiredTask)
		}
	}
	if requiredTask, ok := videoTaskByCapabilityID[cap.ID]; ok {
		if provider == "" {
			return fmt.Errorf("%s: extra.provider is required for %s", ctx, cap.ID)
		}
		videoRaw, ok := cap.Extra["video"]
		if !ok {
			return fmt.Errorf("%s: extra.video is required for %s", ctx, cap.ID)
		}
		videoExtra, ok := videoRaw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: extra.video must be a map for %s", ctx, cap.ID)
		}
		task := strings.TrimSpace(asString(videoExtra["task"]))
		if task == "" {
			return fmt.Errorf("%s: extra.video.task is required for %s", ctx, cap.ID)
		}
		if task != requiredTask {
			return fmt.Errorf("%s: extra.video.task %q is invalid for %s; want %q", ctx, task, cap.ID, requiredTask)
		}
	}
	if requiredTask, ok := vtuberTaskByCapabilityID[cap.ID]; ok {
		if provider == "" {
			return fmt.Errorf("%s: extra.provider is required for %s", ctx, cap.ID)
		}
		vtuberRaw, ok := cap.Extra["vtuber"]
		if !ok {
			return fmt.Errorf("%s: extra.vtuber is required for %s", ctx, cap.ID)
		}
		vtuberExtra, ok := vtuberRaw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: extra.vtuber must be a map for %s", ctx, cap.ID)
		}
		task := strings.TrimSpace(asString(vtuberExtra["task"]))
		if task == "" {
			return fmt.Errorf("%s: extra.vtuber.task is required for %s", ctx, cap.ID)
		}
		if task != requiredTask {
			return fmt.Errorf("%s: extra.vtuber.task %q is invalid for %s; want %q", ctx, task, cap.ID, requiredTask)
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
