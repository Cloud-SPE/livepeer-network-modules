package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var (
	protocolRE   = regexp.MustCompile(`^[a-z][a-z0-9-]*/v[0-9]+$`)
	schemaTagRE  = regexp.MustCompile(`^[a-z][a-z0-9-]*/v[0-9]+$`)
	ethAddressRE = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	priceWeiRE   = regexp.MustCompile(`^[0-9]+$`)
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

	if len(c.Capabilities) == 0 {
		return fmt.Errorf("capabilities: must declare at least one")
	}

	seenPublished := make(map[string]int, len(c.Capabilities))
	seenBackendsPerTuple := make(map[string]map[string]struct{}, len(c.Capabilities))
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

		if !protocolRE.MatchString(cap.Protocol) {
			return fmt.Errorf("%s: protocol must match <name>/v<major> (got %q)", ctx, cap.Protocol)
		}
		switch {
		case strings.HasPrefix(cap.Protocol, "paid-job/"):
			if cap.Session != nil {
				return fmt.Errorf("%s: session axes are invalid on a paid-job offering", ctx)
			}
			if cap.Job == nil || len(cap.Job.Transports) == 0 {
				return fmt.Errorf("%s: job.transports is required for paid-job offerings", ctx)
			}
			seenT := map[string]bool{}
			for _, tr := range cap.Job.Transports {
				switch tr {
				case "unary", "stream", "multipart":
				default:
					return fmt.Errorf("%s: job.transports entry %q must be unary|stream|multipart", ctx, tr)
				}
				if seenT[tr] {
					return fmt.Errorf("%s: job.transports entry %q duplicated", ctx, tr)
				}
				seenT[tr] = true
			}
		case strings.HasPrefix(cap.Protocol, "paid-session/"):
			if cap.Job != nil {
				return fmt.Errorf("%s: job axes are invalid on a paid-session offering", ctx)
			}
			if cap.Session == nil {
				return fmt.Errorf("%s: session block is required for paid-session offerings", ctx)
			}
			if !schemaTagRE.MatchString(cap.Session.DescriptorSchema) {
				return fmt.Errorf("%s: session.descriptor_schema must match <name>/v<major> (got %q)", ctx, cap.Session.DescriptorSchema)
			}
			if cap.Session.Runner.CreatePath == "" || cap.Session.Runner.TerminatePath == "" {
				return fmt.Errorf("%s: session.runner.create_path and terminate_path are required", ctx)
			}
			if c.SessionStore.Path == "" {
				return fmt.Errorf("%s: session_store must be configured when a paid-session capability is declared", ctx)
			}
			if c.ExternalBaseURL == "" {
				return fmt.Errorf("%s: external_base_url must be configured when a paid-session capability is declared", ctx)
			}
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
		if cap.Backend.MaxInFlight < 0 {
			return fmt.Errorf("%s: backend.max_in_flight must be >= 0", ctx)
		}
		if cap.Backend.QueueLimit < 0 {
			return fmt.Errorf("%s: backend.queue_limit must be >= 0", ctx)
		}
		if cap.Backend.ID == "" && cap.Backend.URL != "" {
			cap.Backend.ID = cap.Backend.URL
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
			if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "worker" {
				return fmt.Errorf("%s: backend.url scheme must be http, https, or worker (got %q)", ctx, u.Scheme)
			}
		case "ffmpeg-subprocess":
			if cap.Backend.Profile == "" {
				return fmt.Errorf("%s: backend.profile is required for transport=ffmpeg-subprocess", ctx)
			}
			if !validEncoderProfiles[cap.Backend.Profile] {
				return fmt.Errorf("%s: backend.profile %q is not one of %v", ctx, cap.Backend.Profile, encoderProfileList())
			}
		case "session-runner":
			if cap.Backend.SessionRunner == nil {
				return fmt.Errorf("%s: backend.session_runner is required for transport=session-runner", ctx)
			}
			if strings.TrimSpace(cap.Backend.SessionRunner.Image) == "" {
				return fmt.Errorf("%s: backend.session_runner.image is required for transport=session-runner", ctx)
			}
		default:
			return fmt.Errorf("%s: backend.transport %q is not yet supported (only 'http', 'ffmpeg-subprocess', or 'session-runner' in v0.1)", ctx, cap.Backend.Transport)
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

		if previousIndex, ok := seenPublished[key]; ok {
			if err := validateRepeatedPublishedTuple(c.Capabilities[previousIndex], *cap, ctx); err != nil {
				return err
			}
		} else {
			seenPublished[key] = i
		}
		if seenBackendsPerTuple[key] == nil {
			seenBackendsPerTuple[key] = map[string]struct{}{}
		}
		if cap.Backend.ID != "" {
			if _, dup := seenBackendsPerTuple[key][cap.Backend.ID]; dup {
				return fmt.Errorf("%s: duplicate backend.id %q under published tuple %s/%s", ctx, cap.Backend.ID, cap.ID, cap.OfferingID)
			}
			seenBackendsPerTuple[key][cap.Backend.ID] = struct{}{}
		}
	}

	return nil
}

func validateRepeatedPublishedTuple(previous, current Capability, ctx string) error {
	if previous.Protocol != current.Protocol {
		return fmt.Errorf("%s: repeated published tuple must reuse the same interaction_mode", ctx)
	}
	if previous.WorkUnit.Name != current.WorkUnit.Name {
		return fmt.Errorf("%s: repeated published tuple must reuse the same work_unit.name", ctx)
	}
	if !jsonMapsEqual(previous.WorkUnit.Extractor, current.WorkUnit.Extractor) {
		return fmt.Errorf("%s: repeated published tuple must reuse the same work_unit.extractor", ctx)
	}
	if previous.Price != current.Price {
		return fmt.Errorf("%s: repeated published tuple must reuse the same price", ctx)
	}
	if !jsonMapsEqual(previous.Extra, current.Extra) {
		return fmt.Errorf("%s: repeated published tuple must reuse the same extra metadata", ctx)
	}
	if !jsonMapsEqual(previous.Constraints, current.Constraints) {
		return fmt.Errorf("%s: repeated published tuple must reuse the same constraints", ctx)
	}
	return nil
}

func jsonMapsEqual(left, right map[string]any) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	leftJSON, err := stableJSON(left)
	if err != nil {
		return false
	}
	rightJSON, err := stableJSON(right)
	if err != nil {
		return false
	}
	return leftJSON == rightJSON
}

func stableJSON(v any) (string, error) {
	normalized := normalizeForJSON(v)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func normalizeForJSON(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for k := range typed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(typed))
		for _, k := range keys {
			out[k] = normalizeForJSON(typed[k])
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, normalizeForJSON(item))
		}
		return out
	default:
		return typed
	}
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
