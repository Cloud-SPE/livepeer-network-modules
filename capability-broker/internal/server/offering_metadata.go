package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server/registry"
)

const kokoroOptionsPath = "/openai-audio-speech/options"
const (
	audioTranscriptionsOptionsPath = "/openai-audio-transcriptions/options"
	audioSpeechTask                = "speech"
	audioTranscriptionTask         = "transcription"
	videoTranscodePresetsPath      = "/v1/video/transcode/presets"
	videoABRPresetsPath            = "/v1/video/transcode/abr/presets"
	vtuberOptionsPath              = "/options"
)

type kokoroOptionsResponse struct {
	Task    string `json:"task"`
	Formats struct {
		Output []string `json:"output"`
	} `json:"formats"`
	DefaultVoice string `json:"default_voice"`
	Voices       struct {
		Native  []string          `json:"native"`
		Aliases map[string]string `json:"aliases"`
	} `json:"voices"`
}

type openAIAudioOptionsResponse struct {
	Task    string `json:"task"`
	Formats struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"formats"`
}

type videoTranscodePresetsResponse struct {
	Presets   []videoTranscodePreset `json:"presets"`
	GPU       string                 `json:"gpu"`
	GPUVendor string                 `json:"gpu_vendor"`
	Count     int                    `json:"count"`
}

type videoTranscodePreset struct {
	Name       string `json:"name"`
	VideoCodec string `json:"video_codec"`
}

type videoABRPresetsResponse struct {
	Presets   []videoABRPreset `json:"presets"`
	GPU       string           `json:"gpu"`
	GPUVendor string           `json:"gpu_vendor"`
	Count     int              `json:"count"`
}

type videoABRPreset struct {
	Name       string              `json:"name"`
	Format     string              `json:"format"`
	Renditions []videoABRRendition `json:"renditions"`
}

type videoABRRendition struct {
	Video *struct {
		Codec string `json:"codec"`
	} `json:"video,omitempty"`
}

type vtuberOptionsResponse struct {
	Capabilities  []string       `json:"capabilities"`
	Renderer      string         `json:"renderer"`
	Task          string         `json:"task"`
	ControlSchema string         `json:"control_schema"`
	MediaSchema   string         `json:"media_schema"`
	Features      map[string]any `json:"features"`
}

func hydrateRunnerMetadata(ctx context.Context, cfg *config.Config) {
	client := &http.Client{Timeout: 2 * time.Second}
	hydrateRunnerMetadataWithClient(ctx, client, cfg)
}

func hydrateRunnerMetadataWithClient(ctx context.Context, client *http.Client, cfg *config.Config) {
	for i := range cfg.Capabilities {
		cap := &cfg.Capabilities[i]
		if cap.ID == "openai:audio-speech" && cap.Backend.Transport == "http" {
			if err := hydrateKokoroVoices(ctx, client, cap); err != nil {
				log.Printf("registry metadata hydrate skipped for %s/%s: %v", cap.ID, cap.OfferingID, err)
			}
		}
		if cap.ID == "openai:audio-transcriptions" && cap.Backend.Transport == "http" {
			if err := hydrateOpenAIAudioOptions(ctx, client, cap, audioTranscriptionsOptionsPath); err != nil {
				log.Printf("registry metadata hydrate skipped for %s/%s: %v", cap.ID, cap.OfferingID, err)
			}
		}
		if cap.ID == "video:transcode.vod" && cap.Backend.Transport == "http" {
			if err := hydrateVideoTranscodeMetadata(ctx, client, cap); err != nil {
				log.Printf("registry metadata hydrate skipped for %s/%s: %v", cap.ID, cap.OfferingID, err)
			}
		}
		if cap.ID == "video:transcode.abr" && cap.Backend.Transport == "http" {
			if err := hydrateVideoABRMetadata(ctx, client, cap); err != nil {
				log.Printf("registry metadata hydrate skipped for %s/%s: %v", cap.ID, cap.OfferingID, err)
			}
		}
		if cap.ID == "livepeer:vtuber-session" && cap.Backend.Transport == "http" {
			if err := hydrateVTuberMetadata(ctx, client, cap); err != nil {
				log.Printf("registry metadata hydrate skipped for %s/%s: %v", cap.ID, cap.OfferingID, err)
			}
		}
	}
}

type metadataCatalog struct {
	mu      sync.RWMutex
	byOffer map[string]map[string]any
	status  map[string]metadataRefreshStatus
}

func newMetadataCatalog() *metadataCatalog {
	return &metadataCatalog{
		byOffer: make(map[string]map[string]any),
		status:  make(map[string]metadataRefreshStatus),
	}
}

func (c *metadataCatalog) ExtraFor(capabilityID, offeringID string) map[string]any {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneMap(c.byOffer[metadataCatalogKey(capabilityID, offeringID)])
}

func (c *metadataCatalog) StatusFor(capabilityID, offeringID string) (registry.MetadataStatus, bool) {
	if c == nil {
		return registry.MetadataStatus{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	st, ok := c.status[metadataCatalogKey(capabilityID, offeringID)]
	if !ok {
		return registry.MetadataStatus{}, false
	}
	return registry.MetadataStatus{
		Provider:      st.Provider,
		Applicable:    st.Applicable,
		LastAttemptAt: st.LastAttemptAt,
		LastSuccessAt: st.LastSuccessAt,
		LastError:     st.LastError,
		LastResult:    st.LastResult,
	}, true
}

func (c *metadataCatalog) Set(capabilityID, offeringID string, extra map[string]any) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := metadataCatalogKey(capabilityID, offeringID)
	if len(extra) == 0 {
		delete(c.byOffer, key)
		return
	}
	c.byOffer[key] = cloneMap(extra)
}

func (c *metadataCatalog) SetStatus(capabilityID, offeringID string, st metadataRefreshStatus) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status[metadataCatalogKey(capabilityID, offeringID)] = st
}

func metadataCatalogKey(capabilityID, offeringID string) string {
	return capabilityID + "|" + offeringID
}

type metadataRefreshStatus struct {
	Provider      string
	Applicable    bool
	LastAttemptAt time.Time
	LastSuccessAt time.Time
	LastError     string
	LastResult    string
}

func refreshMetadataCatalog(ctx context.Context, client *http.Client, cfg *config.Config, catalog *metadataCatalog) {
	for i := range cfg.Capabilities {
		cap := &cfg.Capabilities[i]
		attemptedAt := time.Now().UTC()
		discovered, applicable, provider, err := discoverOpenAIBackendMetadata(ctx, client, cap)
		if !applicable {
			continue
		}
		status := metadataRefreshStatus{
			Provider:      provider,
			Applicable:    true,
			LastAttemptAt: attemptedAt,
		}
		if err != nil {
			status.LastError = err.Error()
			status.LastResult = "error"
			catalog.SetStatus(cap.ID, cap.OfferingID, status)
			log.Printf("registry metadata refresh skipped for %s/%s: %v", cap.ID, cap.OfferingID, err)
			continue
		}
		status.LastSuccessAt = attemptedAt
		if len(discovered) == 0 {
			status.LastResult = "model_not_found"
		} else {
			status.LastResult = "enriched"
		}
		catalog.Set(cap.ID, cap.OfferingID, discovered)
		catalog.SetStatus(cap.ID, cap.OfferingID, status)
	}
}

type openAIModelsResponse struct {
	Data []openAIModelRecord `json:"data"`
}

type openAIModelRecord struct {
	ID   string `json:"id"`
	Root string `json:"root"`
}

func discoverOpenAIBackendMetadata(ctx context.Context, client *http.Client, cap *config.Capability) (map[string]any, bool, string, error) {
	if !strings.HasPrefix(cap.ID, "openai:") {
		return nil, false, "", nil
	}
	provider := strings.TrimSpace(asString(cap.Extra["provider"]))
	if provider != "vllm" && provider != "ollama" {
		return nil, false, provider, nil
	}
	modelName, ok := openAIConfiguredModel(cap)
	if !ok {
		return nil, true, provider, nil
	}

	modelsURL, err := deriveOpenAIModelsURL(cap.Backend.URL)
	if err != nil {
		return nil, true, provider, err
	}
	model, err := fetchOpenAIModelRecord(ctx, client, modelsURL, modelName)
	if err != nil {
		return nil, true, provider, err
	}
	if model == nil {
		return nil, true, provider, nil
	}

	discovered := map[string]any{}
	fillDiscoveredString(cap.Extra, discovered, "served_model_name", model.ID)
	if backendModel := strings.TrimSpace(model.Root); backendModel != "" && backendModel != model.ID {
		fillDiscoveredString(cap.Extra, discovered, "backend_model", backendModel)
	}
	fillOpenAIFeatures(cap, discovered)
	return discovered, true, provider, nil
}

func hydrateKokoroVoices(ctx context.Context, client *http.Client, cap *config.Capability) error {
	optionsURL, err := deriveOptionsURL(cap.Backend.URL, kokoroOptionsPath)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, optionsURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &unexpectedStatusError{statusCode: resp.StatusCode}
	}

	var payload kokoroOptionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}
	if len(payload.Voices.Native) == 0 && len(payload.Voices.Aliases) == 0 && payload.DefaultVoice == "" {
		return nil
	}

	if cap.Extra == nil {
		cap.Extra = make(map[string]any)
	}
	audioExtra := ensureNestedMap(cap.Extra, "audio")
	fillExtraString(audioExtra, "task", payload.Task)
	formats := map[string]any{}
	if len(payload.Formats.Output) > 0 {
		formats["output"] = payload.Formats.Output
	}
	if len(formats) > 0 {
		if _, exists := audioExtra["formats"]; !exists {
			audioExtra["formats"] = formats
		}
	}
	if _, exists := audioExtra["voices"]; !exists {
		audioExtra["voices"] = map[string]any{
			"default": payload.DefaultVoice,
			"native":  payload.Voices.Native,
			"aliases": payload.Voices.Aliases,
		}
	}
	return nil
}

func hydrateOpenAIAudioOptions(ctx context.Context, client *http.Client, cap *config.Capability, optionsPath string) error {
	optionsURL, err := deriveOptionsURL(cap.Backend.URL, optionsPath)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, optionsURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &unexpectedStatusError{statusCode: resp.StatusCode}
	}

	var payload openAIAudioOptionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}

	if cap.Extra == nil {
		cap.Extra = make(map[string]any)
	}
	audioExtra := ensureNestedMap(cap.Extra, "audio")
	fillExtraString(audioExtra, "task", payload.Task)
	if len(payload.Formats.Input) > 0 || len(payload.Formats.Output) > 0 {
		if _, exists := audioExtra["formats"]; !exists {
			formats := map[string]any{}
			if len(payload.Formats.Input) > 0 {
				formats["input"] = payload.Formats.Input
			}
			if len(payload.Formats.Output) > 0 {
				formats["output"] = payload.Formats.Output
			}
			audioExtra["formats"] = formats
		}
	}
	return nil
}

func hydrateVideoTranscodeMetadata(ctx context.Context, client *http.Client, cap *config.Capability) error {
	presetsURL, err := deriveOptionsURL(cap.Backend.URL, videoTranscodePresetsPath)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, presetsURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &unexpectedStatusError{statusCode: resp.StatusCode}
	}

	var payload videoTranscodePresetsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}

	if cap.Extra == nil {
		cap.Extra = make(map[string]any)
	}
	videoExtra := ensureNestedMap(cap.Extra, "video")
	fillVideoStringSlice(videoExtra, "presets", namedPresets(payload.Presets))
	fillVideoStringSlice(videoExtra, "codecs", uniqueVideoCodecs(payload.Presets))
	fillVideoStringSlice(videoExtra, "packaging", []string{"mp4"})
	fillVideoHardwareVendor(videoExtra, payload.GPUVendor)
	return nil
}

func hydrateVideoABRMetadata(ctx context.Context, client *http.Client, cap *config.Capability) error {
	presetsURL, err := deriveOptionsURL(cap.Backend.URL, videoABRPresetsPath)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, presetsURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &unexpectedStatusError{statusCode: resp.StatusCode}
	}

	var payload videoABRPresetsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}

	if cap.Extra == nil {
		cap.Extra = make(map[string]any)
	}
	videoExtra := ensureNestedMap(cap.Extra, "video")
	fillVideoStringSlice(videoExtra, "presets", namedABRPresets(payload.Presets))
	fillVideoStringSlice(videoExtra, "codecs", uniqueABRVideoCodecs(payload.Presets))
	fillVideoStringSlice(videoExtra, "packaging", uniqueABRPackaging(payload.Presets))
	fillVideoHardwareVendor(videoExtra, payload.GPUVendor)
	return nil
}

func hydrateVTuberMetadata(ctx context.Context, client *http.Client, cap *config.Capability) error {
	optionsURL, err := deriveOptionsURL(cap.Backend.URL, vtuberOptionsPath)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, optionsURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &unexpectedStatusError{statusCode: resp.StatusCode}
	}

	var payload vtuberOptionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}

	if cap.Extra == nil {
		cap.Extra = make(map[string]any)
	}
	vtuberExtra := ensureNestedMap(cap.Extra, "vtuber")
	fillExtraString(vtuberExtra, "task", payload.Task)
	fillExtraString(vtuberExtra, "control_schema", payload.ControlSchema)
	fillExtraString(vtuberExtra, "media_schema", payload.MediaSchema)
	if _, exists := vtuberExtra["features"]; !exists {
		if features := boolMap(payload.Features); len(features) > 0 {
			vtuberExtra["features"] = features
		}
	}
	return nil
}

func fetchOpenAIModelRecord(ctx context.Context, client *http.Client, modelsURL, modelName string) (*openAIModelRecord, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &unexpectedStatusError{statusCode: resp.StatusCode}
	}

	var payload openAIModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	for _, item := range payload.Data {
		if item.ID == modelName {
			model := item
			return &model, nil
		}
	}
	return nil, nil
}

func deriveOptionsURL(rawBackendURL, optionsPath string) (string, error) {
	u, err := url.Parse(rawBackendURL)
	if err != nil {
		return "", err
	}
	return (&url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
		Path:   optionsPath,
	}).String(), nil
}

func deriveOpenAIModelsURL(rawBackendURL string) (string, error) {
	u, err := url.Parse(rawBackendURL)
	if err != nil {
		return "", err
	}
	return (&url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
		Path:   "/v1/models",
	}).String(), nil
}

func openAIConfiguredModel(cap *config.Capability) (string, bool) {
	if cap.Extra == nil {
		return "", false
	}
	openaiRaw, ok := cap.Extra["openai"]
	if !ok {
		return "", false
	}
	openaiMap, ok := openaiRaw.(map[string]any)
	if !ok {
		return "", false
	}
	model := strings.TrimSpace(asString(openaiMap["model"]))
	if model == "" {
		return "", false
	}
	return model, true
}

func fillExtraString(extra map[string]any, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	if strings.TrimSpace(asString(extra[key])) != "" {
		return
	}
	extra[key] = value
}

func ensureNestedMap(extra map[string]any, key string) map[string]any {
	nested, _ := extra[key].(map[string]any)
	if nested == nil {
		nested = map[string]any{}
		extra[key] = nested
	}
	return nested
}

func fillVideoStringSlice(videoExtra map[string]any, key string, values []string) {
	if len(values) == 0 {
		return
	}
	if _, exists := videoExtra[key]; exists {
		return
	}
	videoExtra[key] = values
}

func fillVideoHardwareVendor(videoExtra map[string]any, gpuVendor string) {
	gpuVendor = strings.TrimSpace(gpuVendor)
	if gpuVendor == "" {
		return
	}
	hardware := ensureNestedMap(videoExtra, "hardware")
	fillExtraString(hardware, "gpu_vendor", gpuVendor)
}

func boolMap(raw map[string]any) map[string]bool {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]bool)
	for key, value := range raw {
		boolean, ok := value.(bool)
		if !ok {
			continue
		}
		out[key] = boolean
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func namedPresets(presets []videoTranscodePreset) []string {
	out := make([]string, 0, len(presets))
	seen := make(map[string]struct{}, len(presets))
	for _, preset := range presets {
		name := strings.TrimSpace(preset.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func uniqueVideoCodecs(presets []videoTranscodePreset) []string {
	out := make([]string, 0, len(presets))
	seen := make(map[string]struct{}, len(presets))
	for _, preset := range presets {
		codec := strings.TrimSpace(preset.VideoCodec)
		if codec == "" {
			continue
		}
		if _, ok := seen[codec]; ok {
			continue
		}
		seen[codec] = struct{}{}
		out = append(out, codec)
	}
	return out
}

func namedABRPresets(presets []videoABRPreset) []string {
	out := make([]string, 0, len(presets))
	seen := make(map[string]struct{}, len(presets))
	for _, preset := range presets {
		name := strings.TrimSpace(preset.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func uniqueABRVideoCodecs(presets []videoABRPreset) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, preset := range presets {
		for _, rendition := range preset.Renditions {
			if rendition.Video == nil {
				continue
			}
			codec := strings.TrimSpace(rendition.Video.Codec)
			if codec == "" {
				continue
			}
			if _, ok := seen[codec]; ok {
				continue
			}
			seen[codec] = struct{}{}
			out = append(out, codec)
		}
	}
	return out
}

func uniqueABRPackaging(presets []videoABRPreset) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, preset := range presets {
		format := strings.TrimSpace(preset.Format)
		if format == "" {
			continue
		}
		if _, ok := seen[format]; ok {
			continue
		}
		seen[format] = struct{}{}
		out = append(out, format)
	}
	return out
}

func fillDiscoveredString(base, discovered map[string]any, key, value string) {
	if strings.TrimSpace(asString(base[key])) != "" {
		return
	}
	fillExtraString(discovered, key, value)
}

func fillOpenAIFeatures(cap *config.Capability, discovered map[string]any) {
	featureKey, featureValue, ok := inferredOpenAIFeature(cap.ID)
	if !ok {
		return
	}
	existingFeatures, _ := cap.Extra["features"].(map[string]any)
	if _, exists := existingFeatures[featureKey]; exists {
		return
	}
	features, _ := discovered["features"].(map[string]any)
	if features == nil {
		features = map[string]any{}
		discovered["features"] = features
	}
	if _, exists := features[featureKey]; exists {
		return
	}
	features[featureKey] = featureValue
}

func inferredOpenAIFeature(capabilityID string) (string, bool, bool) {
	switch capabilityID {
	case "openai:chat-completions":
		return "streaming", true, true
	case "openai:embeddings":
		return "embeddings", true, true
	default:
		return "", false, false
	}
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

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if nested, ok := v.(map[string]any); ok {
			out[k] = cloneMap(nested)
			continue
		}
		out[k] = v
	}
	return out
}

type unexpectedStatusError struct {
	statusCode int
}

func (e *unexpectedStatusError) Error() string {
	return fmt.Sprintf("unexpected HTTP status %d", e.statusCode)
}
