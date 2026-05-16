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
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/observability"
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
		startedAt := time.Now()
		family := metadataFamily(cap.ID)
		discovered, applicable, provider, result, err := discoverCapabilityMetadata(ctx, client, cap)
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
			if result != "" {
				status.LastResult = result
			} else {
				status.LastResult = "error"
			}
			catalog.SetStatus(cap.ID, cap.OfferingID, status)
			observability.RecordMetadataRefresh(
				family,
				cap.ID,
				cap.OfferingID,
				provider,
				status.LastResult,
				time.Since(startedAt).Seconds(),
				attemptedAt,
				time.Time{},
			)
			log.Printf("registry metadata refresh skipped for %s/%s: %v", cap.ID, cap.OfferingID, err)
			continue
		}
		status.LastSuccessAt = attemptedAt
		if result == "" {
			if len(discovered) == 0 {
				status.LastResult = "empty"
			} else {
				status.LastResult = "enriched"
			}
		} else {
			status.LastResult = result
		}
		catalog.Set(cap.ID, cap.OfferingID, discovered)
		catalog.SetStatus(cap.ID, cap.OfferingID, status)
		observability.RecordMetadataRefresh(
			family,
			cap.ID,
			cap.OfferingID,
			provider,
			status.LastResult,
			time.Since(startedAt).Seconds(),
			attemptedAt,
			status.LastSuccessAt,
		)
	}
}

type openAIModelsResponse struct {
	Data []openAIModelRecord `json:"data"`
}

type openAIModelRecord struct {
	ID   string `json:"id"`
	Root string `json:"root"`
}

func discoverCapabilityMetadata(ctx context.Context, client *http.Client, cap *config.Capability) (map[string]any, bool, string, string, error) {
	if discovered, applicable, provider, result, err := discoverOpenAIBackendMetadata(ctx, client, cap); applicable || err != nil {
		return discovered, applicable, provider, result, err
	}
	if discovered, applicable, provider, result, err := discoverAudioMetadata(ctx, client, cap); applicable || err != nil {
		return discovered, applicable, provider, result, err
	}
	if discovered, applicable, provider, result, err := discoverVideoMetadata(ctx, client, cap); applicable || err != nil {
		return discovered, applicable, provider, result, err
	}
	if discovered, applicable, provider, result, err := discoverVTuberMetadata(ctx, client, cap); applicable || err != nil {
		return discovered, applicable, provider, result, err
	}
	return nil, false, strings.TrimSpace(asString(cap.Extra["provider"])), "", nil
}

func discoverOpenAIBackendMetadata(ctx context.Context, client *http.Client, cap *config.Capability) (map[string]any, bool, string, string, error) {
	if !strings.HasPrefix(cap.ID, "openai:") {
		return nil, false, "", "", nil
	}
	provider := strings.TrimSpace(asString(cap.Extra["provider"]))
	if provider != "vllm" && provider != "ollama" {
		return nil, false, provider, "", nil
	}
	modelName, ok := openAIConfiguredModel(cap)
	if !ok {
		return nil, true, provider, "empty", nil
	}

	modelsURL, err := deriveOpenAIModelsURL(cap.Backend.URL)
	if err != nil {
		return nil, true, provider, "models_probe_failed", err
	}
	model, err := fetchOpenAIModelRecord(ctx, client, modelsURL, modelName)
	if err != nil {
		return nil, true, provider, "models_probe_failed", err
	}
	if model == nil {
		return nil, true, provider, "model_not_found", nil
	}

	discovered := map[string]any{}
	fillDiscoveredString(cap.Extra, discovered, "served_model_name", model.ID)
	if backendModel := strings.TrimSpace(model.Root); backendModel != "" && backendModel != model.ID {
		fillDiscoveredString(cap.Extra, discovered, "backend_model", backendModel)
	}
	fillOpenAIFeatures(cap, discovered)
	return discovered, true, provider, "enriched", nil
}

func discoverAudioMetadata(ctx context.Context, client *http.Client, cap *config.Capability) (map[string]any, bool, string, string, error) {
	if cap.Backend.Transport != "http" {
		return nil, false, "", "", nil
	}
	provider := strings.TrimSpace(asString(cap.Extra["provider"]))
	switch cap.ID {
	case "openai:audio-speech":
		payload, err := fetchKokoroOptions(ctx, client, cap.Backend.URL)
		if err != nil {
			return nil, true, provider, "audio_options_probe_failed", err
		}
		discovered := discoveredAudioSpeechExtra(cap.Extra, payload)
		if len(discovered) == 0 {
			return nil, true, provider, "audio_options_empty", nil
		}
		return discovered, true, provider, "enriched", nil
	case "openai:audio-transcriptions":
		payload, err := fetchOpenAIAudioOptions(ctx, client, cap.Backend.URL, audioTranscriptionsOptionsPath)
		if err != nil {
			return nil, true, provider, "audio_options_probe_failed", err
		}
		discovered := discoveredAudioOptionsExtra(cap.Extra, payload)
		if len(discovered) == 0 {
			return nil, true, provider, "audio_options_empty", nil
		}
		return discovered, true, provider, "enriched", nil
	default:
		return nil, false, "", "", nil
	}
}

func discoverVideoMetadata(ctx context.Context, client *http.Client, cap *config.Capability) (map[string]any, bool, string, string, error) {
	if cap.Backend.Transport != "http" {
		return nil, false, "", "", nil
	}
	provider := strings.TrimSpace(asString(cap.Extra["provider"]))
	switch cap.ID {
	case "video:transcode.vod":
		payload, err := fetchVideoTranscodePresets(ctx, client, cap.Backend.URL)
		if err != nil {
			return nil, true, provider, "video_presets_probe_failed", err
		}
		discovered := discoveredVideoTranscodeExtra(cap.Extra, payload)
		if len(discovered) == 0 {
			return nil, true, provider, "video_presets_empty", nil
		}
		return discovered, true, provider, "enriched", nil
	case "video:transcode.abr":
		payload, err := fetchVideoABRPresets(ctx, client, cap.Backend.URL)
		if err != nil {
			return nil, true, provider, "video_presets_probe_failed", err
		}
		discovered := discoveredVideoABRExtra(cap.Extra, payload)
		if len(discovered) == 0 {
			return nil, true, provider, "video_presets_empty", nil
		}
		return discovered, true, provider, "enriched", nil
	default:
		return nil, false, "", "", nil
	}
}

func discoverVTuberMetadata(ctx context.Context, client *http.Client, cap *config.Capability) (map[string]any, bool, string, string, error) {
	if cap.ID != "livepeer:vtuber-session" || cap.Backend.Transport != "http" {
		return nil, false, "", "", nil
	}
	provider := strings.TrimSpace(asString(cap.Extra["provider"]))
	payload, err := fetchVTuberOptions(ctx, client, cap.Backend.URL)
	if err != nil {
		return nil, true, provider, "vtuber_options_probe_failed", err
	}
	discovered := discoveredVTuberExtra(cap.Extra, payload)
	if len(discovered) == 0 {
		return nil, true, provider, "vtuber_options_empty", nil
	}
	return discovered, true, provider, "enriched", nil
}

func hydrateKokoroVoices(ctx context.Context, client *http.Client, cap *config.Capability) error {
	payload, err := fetchKokoroOptions(ctx, client, cap.Backend.URL)
	if err != nil {
		return err
	}
	if len(payload.Voices.Native) == 0 && len(payload.Voices.Aliases) == 0 && payload.DefaultVoice == "" {
		return nil
	}

	if cap.Extra == nil {
		cap.Extra = make(map[string]any)
	}
	mergeDiscoveredExtra(cap.Extra, discoveredAudioSpeechExtra(cap.Extra, payload))
	return nil
}

func hydrateOpenAIAudioOptions(ctx context.Context, client *http.Client, cap *config.Capability, optionsPath string) error {
	payload, err := fetchOpenAIAudioOptions(ctx, client, cap.Backend.URL, optionsPath)
	if err != nil {
		return err
	}

	if cap.Extra == nil {
		cap.Extra = make(map[string]any)
	}
	mergeDiscoveredExtra(cap.Extra, discoveredAudioOptionsExtra(cap.Extra, payload))
	return nil
}

func hydrateVideoTranscodeMetadata(ctx context.Context, client *http.Client, cap *config.Capability) error {
	payload, err := fetchVideoTranscodePresets(ctx, client, cap.Backend.URL)
	if err != nil {
		return err
	}

	if cap.Extra == nil {
		cap.Extra = make(map[string]any)
	}
	mergeDiscoveredExtra(cap.Extra, discoveredVideoTranscodeExtra(cap.Extra, payload))
	return nil
}

func hydrateVideoABRMetadata(ctx context.Context, client *http.Client, cap *config.Capability) error {
	payload, err := fetchVideoABRPresets(ctx, client, cap.Backend.URL)
	if err != nil {
		return err
	}

	if cap.Extra == nil {
		cap.Extra = make(map[string]any)
	}
	mergeDiscoveredExtra(cap.Extra, discoveredVideoABRExtra(cap.Extra, payload))
	return nil
}

func hydrateVTuberMetadata(ctx context.Context, client *http.Client, cap *config.Capability) error {
	payload, err := fetchVTuberOptions(ctx, client, cap.Backend.URL)
	if err != nil {
		return err
	}

	if cap.Extra == nil {
		cap.Extra = make(map[string]any)
	}
	mergeDiscoveredExtra(cap.Extra, discoveredVTuberExtra(cap.Extra, payload))
	return nil
}

func fetchKokoroOptions(ctx context.Context, client *http.Client, backendURL string) (kokoroOptionsResponse, error) {
	optionsURL, err := deriveOptionsURL(backendURL, kokoroOptionsPath)
	if err != nil {
		return kokoroOptionsResponse{}, err
	}
	var payload kokoroOptionsResponse
	if err := fetchJSON(ctx, client, optionsURL, &payload); err != nil {
		return kokoroOptionsResponse{}, err
	}
	return payload, nil
}

func fetchOpenAIAudioOptions(ctx context.Context, client *http.Client, backendURL, optionsPath string) (openAIAudioOptionsResponse, error) {
	optionsURL, err := deriveOptionsURL(backendURL, optionsPath)
	if err != nil {
		return openAIAudioOptionsResponse{}, err
	}
	var payload openAIAudioOptionsResponse
	if err := fetchJSON(ctx, client, optionsURL, &payload); err != nil {
		return openAIAudioOptionsResponse{}, err
	}
	return payload, nil
}

func fetchVideoTranscodePresets(ctx context.Context, client *http.Client, backendURL string) (videoTranscodePresetsResponse, error) {
	presetsURL, err := deriveOptionsURL(backendURL, videoTranscodePresetsPath)
	if err != nil {
		return videoTranscodePresetsResponse{}, err
	}
	var payload videoTranscodePresetsResponse
	if err := fetchJSON(ctx, client, presetsURL, &payload); err != nil {
		return videoTranscodePresetsResponse{}, err
	}
	return payload, nil
}

func fetchVideoABRPresets(ctx context.Context, client *http.Client, backendURL string) (videoABRPresetsResponse, error) {
	presetsURL, err := deriveOptionsURL(backendURL, videoABRPresetsPath)
	if err != nil {
		return videoABRPresetsResponse{}, err
	}
	var payload videoABRPresetsResponse
	if err := fetchJSON(ctx, client, presetsURL, &payload); err != nil {
		return videoABRPresetsResponse{}, err
	}
	return payload, nil
}

func fetchVTuberOptions(ctx context.Context, client *http.Client, backendURL string) (vtuberOptionsResponse, error) {
	optionsURL, err := deriveOptionsURL(backendURL, vtuberOptionsPath)
	if err != nil {
		return vtuberOptionsResponse{}, err
	}
	var payload vtuberOptionsResponse
	if err := fetchJSON(ctx, client, optionsURL, &payload); err != nil {
		return vtuberOptionsResponse{}, err
	}
	return payload, nil
}

func fetchJSON(ctx context.Context, client *http.Client, requestURL string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
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
	return json.NewDecoder(resp.Body).Decode(dst)
}

func discoveredAudioSpeechExtra(base map[string]any, payload kokoroOptionsResponse) map[string]any {
	discovered := map[string]any{}
	audio := map[string]any{}
	fillDiscoveredString(nestedMap(base, "audio"), audio, "task", payload.Task)
	if len(payload.Formats.Output) > 0 {
		formats := map[string]any{}
		if !nestedKeyExists(base, "audio", "formats") {
			formats["output"] = payload.Formats.Output
			audio["formats"] = formats
		}
	}
	if !nestedKeyExists(base, "audio", "voices") {
		audio["voices"] = map[string]any{
			"default": payload.DefaultVoice,
			"native":  payload.Voices.Native,
			"aliases": payload.Voices.Aliases,
		}
	}
	if len(audio) > 0 {
		discovered["audio"] = audio
	}
	return discovered
}

func discoveredAudioOptionsExtra(base map[string]any, payload openAIAudioOptionsResponse) map[string]any {
	discovered := map[string]any{}
	audio := map[string]any{}
	fillDiscoveredString(nestedMap(base, "audio"), audio, "task", payload.Task)
	if !nestedKeyExists(base, "audio", "formats") && (len(payload.Formats.Input) > 0 || len(payload.Formats.Output) > 0) {
		formats := map[string]any{}
		if len(payload.Formats.Input) > 0 {
			formats["input"] = payload.Formats.Input
		}
		if len(payload.Formats.Output) > 0 {
			formats["output"] = payload.Formats.Output
		}
		audio["formats"] = formats
	}
	if len(audio) > 0 {
		discovered["audio"] = audio
	}
	return discovered
}

func discoveredVideoTranscodeExtra(base map[string]any, payload videoTranscodePresetsResponse) map[string]any {
	discovered := map[string]any{}
	video := map[string]any{}
	fillDiscoveredStringSlice(nestedMap(base, "video"), video, "presets", namedPresets(payload.Presets))
	fillDiscoveredStringSlice(nestedMap(base, "video"), video, "codecs", uniqueVideoCodecs(payload.Presets))
	fillDiscoveredStringSlice(nestedMap(base, "video"), video, "packaging", []string{"mp4"})
	if strings.TrimSpace(payload.GPUVendor) != "" && !nestedKeyExists(base, "video", "hardware") {
		video["hardware"] = map[string]any{"gpu_vendor": payload.GPUVendor}
	} else if strings.TrimSpace(payload.GPUVendor) != "" {
		hardwareBase := nestedMap(nestedMap(base, "video"), "hardware")
		hardware := map[string]any{}
		fillDiscoveredString(hardwareBase, hardware, "gpu_vendor", payload.GPUVendor)
		if len(hardware) > 0 {
			video["hardware"] = hardware
		}
	}
	if len(video) > 0 {
		discovered["video"] = video
	}
	return discovered
}

func discoveredVideoABRExtra(base map[string]any, payload videoABRPresetsResponse) map[string]any {
	discovered := map[string]any{}
	video := map[string]any{}
	fillDiscoveredStringSlice(nestedMap(base, "video"), video, "presets", namedABRPresets(payload.Presets))
	fillDiscoveredStringSlice(nestedMap(base, "video"), video, "codecs", uniqueABRVideoCodecs(payload.Presets))
	fillDiscoveredStringSlice(nestedMap(base, "video"), video, "packaging", uniqueABRPackaging(payload.Presets))
	if strings.TrimSpace(payload.GPUVendor) != "" && !nestedKeyExists(base, "video", "hardware") {
		video["hardware"] = map[string]any{"gpu_vendor": payload.GPUVendor}
	} else if strings.TrimSpace(payload.GPUVendor) != "" {
		hardwareBase := nestedMap(nestedMap(base, "video"), "hardware")
		hardware := map[string]any{}
		fillDiscoveredString(hardwareBase, hardware, "gpu_vendor", payload.GPUVendor)
		if len(hardware) > 0 {
			video["hardware"] = hardware
		}
	}
	if len(video) > 0 {
		discovered["video"] = video
	}
	return discovered
}

func discoveredVTuberExtra(base map[string]any, payload vtuberOptionsResponse) map[string]any {
	discovered := map[string]any{}
	vtuber := map[string]any{}
	vtuberBase := nestedMap(base, "vtuber")
	fillDiscoveredString(vtuberBase, vtuber, "task", payload.Task)
	fillDiscoveredString(vtuberBase, vtuber, "control_schema", payload.ControlSchema)
	fillDiscoveredString(vtuberBase, vtuber, "media_schema", payload.MediaSchema)
	if !nestedKeyExists(base, "vtuber", "features") {
		if features := boolMap(payload.Features); len(features) > 0 {
			vtuber["features"] = features
		}
	}
	if len(vtuber) > 0 {
		discovered["vtuber"] = vtuber
	}
	return discovered
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

func fillDiscoveredStringSlice(base, discovered map[string]any, key string, values []string) {
	if len(values) == 0 {
		return
	}
	if _, exists := base[key]; exists {
		return
	}
	discovered[key] = values
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

func nestedMap(base map[string]any, key string) map[string]any {
	if base == nil {
		return nil
	}
	nested, _ := base[key].(map[string]any)
	return nested
}

func nestedKeyExists(base map[string]any, keys ...string) bool {
	current := base
	for i, key := range keys {
		if current == nil {
			return false
		}
		value, ok := current[key]
		if !ok {
			return false
		}
		if i == len(keys)-1 {
			return true
		}
		next, ok := value.(map[string]any)
		if !ok {
			return false
		}
		current = next
	}
	return false
}

func mergeDiscoveredExtra(base, discovered map[string]any) {
	for key, value := range discovered {
		if nested, ok := value.(map[string]any); ok {
			dest := ensureNestedMap(base, key)
			mergeDiscoveredExtra(dest, nested)
			continue
		}
		if _, exists := base[key]; exists {
			continue
		}
		base[key] = value
	}
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

func metadataFamily(capabilityID string) string {
	switch {
	case strings.HasPrefix(capabilityID, "openai:audio-"):
		return "audio"
	case strings.HasPrefix(capabilityID, "openai:"):
		return "openai"
	case strings.HasPrefix(capabilityID, "video:"):
		return "video"
	case capabilityID == "livepeer:vtuber-session":
		return "vtuber"
	default:
		return "other"
	}
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
