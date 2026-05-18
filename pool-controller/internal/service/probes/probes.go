package probes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type ApplyFunc func(types.SyntheticProbeObservation) (types.BackendSelectionState, error)

type Runner struct {
	client *http.Client
}

type RunSummary struct {
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
	Applied    int           `json:"applied"`
	Skipped    int           `json:"skipped"`
	Failed     int           `json:"failed"`
	Succeeded  int           `json:"succeeded"`
	Results    []ProbeResult `json:"results"`
}

type ProbeResult struct {
	MemberEthAddress string `json:"member_eth_address"`
	BackendID        string `json:"backend_id"`
	CapabilityID     string `json:"capability_id"`
	OfferingID       string `json:"offering_id"`
	Status           string `json:"status"`
	Reason           string `json:"reason,omitempty"`
}

func NewRunner(timeout time.Duration) *Runner {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &Runner{
		client: &http.Client{Timeout: timeout},
	}
}

func (r *Runner) RunOnce(ctx context.Context, cfg *config.Config, apply ApplyFunc) (RunSummary, error) {
	summary := RunSummary{
		StartedAt: time.Now().UTC(),
		Results:   make([]ProbeResult, 0),
	}
	defer func() {
		summary.FinishedAt = time.Now().UTC()
	}()
	if cfg == nil {
		return summary, fmt.Errorf("config is required")
	}
	for _, member := range cfg.Members {
		for _, backend := range member.Backends {
			for _, offering := range backend.Offerings {
				result := ProbeResult{
					MemberEthAddress: member.EthAddress,
					BackendID:        backend.ID,
					CapabilityID:     offering.CapabilityID,
					OfferingID:       offering.OfferingID,
				}
				observation, status, reason, err := r.probeOffering(ctx, member, backend, offering)
				if err != nil {
					result.Status = "failed"
					result.Reason = err.Error()
					summary.Failed++
					summary.Results = append(summary.Results, result)
					continue
				}
				if status == "skipped" {
					result.Status = status
					result.Reason = reason
					summary.Skipped++
					summary.Results = append(summary.Results, result)
					continue
				}
				result.Status = status
				result.Reason = reason
				summary.Results = append(summary.Results, result)
				if _, err := apply(observation); err != nil {
					summary.Failed++
					result.Status = "failed"
					result.Reason = err.Error()
					summary.Results[len(summary.Results)-1] = result
					continue
				}
				summary.Applied++
				if observation.Success {
					summary.Succeeded++
				} else {
					summary.Failed++
				}
				summary.Results[len(summary.Results)-1] = result
			}
		}
	}
	return summary, nil
}

func (r *Runner) probeOffering(ctx context.Context, member config.Member, backend config.Backend, offering config.Offering) (types.SyntheticProbeObservation, string, string, error) {
	base := types.SyntheticProbeObservation{
		MemberEthAddress: member.EthAddress,
		BackendID:        backend.ID,
		CapabilityID:     offering.CapabilityID,
		OfferingID:       offering.OfferingID,
		ObservedAt:       time.Now().UTC(),
	}
	if backend.Transport != "http" || strings.TrimSpace(backend.URL) == "" {
		return base, "skipped", "unsupported_transport", nil
	}
	switch {
	case offering.CapabilityID == "openai:chat-completions":
		ok, reason, err := r.runOpenAIJSONProbe(ctx, backend, offering, map[string]any{
			"model":      modelName(backend, offering),
			"messages":   []map[string]string{{"role": "user", "content": "ping"}},
			"max_tokens": 1,
			"stream":     false,
		}, func(body map[string]any) bool {
			choices, _ := body["choices"].([]any)
			return len(choices) > 0
		})
		base.Success = ok
		base.Result = reason
		return base, statusFromSuccess(ok), reason, err
	case offering.CapabilityID == "openai:embeddings":
		ok, reason, err := r.runOpenAIJSONProbe(ctx, backend, offering, map[string]any{
			"model": modelName(backend, offering),
			"input": "ping",
		}, func(body map[string]any) bool {
			data, _ := body["data"].([]any)
			return len(data) > 0
		})
		base.Success = ok
		base.Result = reason
		return base, statusFromSuccess(ok), reason, err
	case strings.HasPrefix(offering.CapabilityID, "openai:audio-"):
		ok, reason, err := r.runOpenAIAudioProbe(ctx, backend, offering)
		if !ok && err == nil && reason == "audio_probe_not_implemented" {
			base.Success = false
			base.Result = reason
			return base, "skipped", reason, nil
		}
		base.Success = ok
		base.Result = reason
		return base, statusFromSuccess(ok), reason, err
	case offering.CapabilityID == "video:transcode.abr":
		ok, reason, err := r.runVideoABRProbe(ctx, backend)
		base.Success = ok
		base.Result = reason
		return base, statusFromSuccess(ok), reason, err
	default:
		return base, "skipped", "capability_out_of_scope", nil
	}
}

func (r *Runner) runOpenAIAudioProbe(ctx context.Context, backend config.Backend, offering config.Offering) (bool, string, error) {
	switch inferOpenAIAudioProbeFamily(offering) {
	case "multipart":
		return r.runOpenAIAudioMultipartProbe(ctx, backend, offering)
	case "speech":
		return r.runOpenAISpeechProbe(ctx, backend, offering)
	default:
		return false, "audio_probe_not_implemented", nil
	}
}

func inferOpenAIAudioProbeFamily(offering config.Offering) string {
	capabilityID := strings.TrimSpace(offering.CapabilityID)
	switch {
	case capabilityID == "openai:audio-transcriptions",
		capabilityID == "openai:audio-translations",
		strings.Contains(capabilityID, "transcription"),
		strings.Contains(capabilityID, "translation"),
		strings.Contains(capabilityID, "stt"):
		return "multipart"
	case capabilityID == "openai:audio-speech",
		strings.Contains(capabilityID, "speech"),
		strings.Contains(capabilityID, "tts"):
		return "speech"
	}
	switch strings.TrimSpace(offering.InteractionMode) {
	case "http-multipart@v0":
		return "multipart"
	case "http-reqresp@v0":
		return "speech"
	default:
		return ""
	}
}

func (r *Runner) runOpenAIJSONProbe(ctx context.Context, backend config.Backend, offering config.Offering, payload map[string]any, validate func(map[string]any) bool) (bool, string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, backend.URL, bytes.NewReader(raw))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := applyBackendAuth(req, backend.Auth); err != nil {
		return false, "", err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return false, "probe_transport_error", nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Sprintf("probe_http_%d", resp.StatusCode), nil
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, "probe_decode_error", nil
	}
	if !validate(body) {
		return false, "probe_invalid_response", nil
	}
	return true, "probe_ok", nil
}

func (r *Runner) runOpenAIAudioMultipartProbe(ctx context.Context, backend config.Backend, offering config.Offering) (bool, string, error) {
	body, contentType, err := buildAudioMultipartBody(modelName(backend, offering))
	if err != nil {
		return false, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, backend.URL, body)
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", contentType)
	if err := applyBackendAuth(req, backend.Auth); err != nil {
		return false, "", err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return false, "probe_transport_error", nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Sprintf("probe_http_%d", resp.StatusCode), nil
	}
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return false, "probe_decode_error", nil
	}
	text, _ := decoded["text"].(string)
	if strings.TrimSpace(text) == "" {
		return false, "probe_invalid_response", nil
	}
	return true, "probe_ok", nil
}

func (r *Runner) runOpenAISpeechProbe(ctx context.Context, backend config.Backend, offering config.Offering) (bool, string, error) {
	raw, err := json.Marshal(map[string]any{
		"model": modelName(backend, offering),
		"input": "ping",
		"voice": "alloy",
	})
	if err != nil {
		return false, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, backend.URL, bytes.NewReader(raw))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := applyBackendAuth(req, backend.Auth); err != nil {
		return false, "", err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return false, "probe_transport_error", nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Sprintf("probe_http_%d", resp.StatusCode), nil
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 32))
	if err != nil {
		return false, "probe_read_error", nil
	}
	if len(payload) == 0 {
		return false, "probe_invalid_response", nil
	}
	return true, "probe_ok", nil
}

func (r *Runner) runVideoABRProbe(ctx context.Context, backend config.Backend) (bool, string, error) {
	target, err := videoABRPresetsURL(backend.URL)
	if err != nil {
		return false, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false, "", err
	}
	if err := applyBackendAuth(req, backend.Auth); err != nil {
		return false, "", err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return false, "probe_transport_error", nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Sprintf("probe_http_%d", resp.StatusCode), nil
	}
	var decoded struct {
		Presets []struct {
			Name string `json:"name"`
		} `json:"presets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return false, "probe_decode_error", nil
	}
	if len(decoded.Presets) == 0 || strings.TrimSpace(decoded.Presets[0].Name) == "" {
		return false, "probe_invalid_response", nil
	}
	return true, "probe_ok", nil
}

func buildAudioMultipartBody(model string) (*bytes.Buffer, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("model", model); err != nil {
		return nil, "", err
	}
	part, err := writer.CreateFormFile("file", "probe.wav")
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(part, bytes.NewReader([]byte("RIFF....WAVEfmt "))); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body, writer.FormDataContentType(), nil
}

func applyBackendAuth(req *http.Request, auth config.AuthConfig) error {
	switch auth.Method {
	case "", "none":
		return nil
	case "bearer":
		secret, err := resolveSecretRef(auth.SecretRef)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+secret)
		return nil
	default:
		return fmt.Errorf("unsupported backend auth method %q", auth.Method)
	}
}

func resolveSecretRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("secret_ref is required")
	}
	if strings.HasPrefix(ref, "env://") {
		key := strings.TrimPrefix(ref, "env://")
		value, ok := os.LookupEnv(key)
		if !ok {
			return "", fmt.Errorf("backend auth env var %q is not set", key)
		}
		if value == "" {
			return "", fmt.Errorf("backend auth env var %q is empty", key)
		}
		return value, nil
	}
	return ref, nil
}

func modelName(backend config.Backend, offering config.Offering) string {
	if value := nestedString(offering.Extra, "openai", "model"); value != "" {
		return value
	}
	if value := nestedString(backend.Extra, "openai", "model"); value != "" {
		return value
	}
	return "probe-model"
}

func videoABRPresetsURL(rawBackendURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawBackendURL))
	if err != nil {
		return "", fmt.Errorf("parse backend url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("backend url must be absolute")
	}
	trimmedPath := strings.TrimSuffix(parsed.Path, "/")
	switch {
	case strings.HasSuffix(trimmedPath, "/v1/video/transcode/abr"):
		parsed.Path = trimmedPath + "/presets"
	case strings.HasSuffix(trimmedPath, "/v1/video/transcode/abr/presets"):
		parsed.Path = trimmedPath
	default:
		parsed.Path = "/v1/video/transcode/abr/presets"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func nestedString(source map[string]any, path ...string) string {
	current := source
	for i, key := range path {
		value, ok := current[key]
		if !ok {
			return ""
		}
		if i == len(path)-1 {
			text, _ := value.(string)
			return strings.TrimSpace(text)
		}
		next, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		current = next
	}
	return ""
}

func statusFromSuccess(ok bool) string {
	if ok {
		return "success"
	}
	return "failure"
}
