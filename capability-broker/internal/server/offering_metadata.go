package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
)

const kokoroOptionsPath = "/openai-audio-speech/options"

type kokoroOptionsResponse struct {
	DefaultVoice string `json:"default_voice"`
	Voices       struct {
		Native  []string          `json:"native"`
		Aliases map[string]string `json:"aliases"`
	} `json:"voices"`
}

func hydrateRunnerMetadata(ctx context.Context, cfg *config.Config) {
	client := &http.Client{Timeout: 2 * time.Second}
	for i := range cfg.Capabilities {
		cap := &cfg.Capabilities[i]
		if cap.ID != "openai:audio-speech" || cap.Backend.Transport != "http" {
			continue
		}
		if err := hydrateKokoroVoices(ctx, client, cap); err != nil {
			log.Printf("registry metadata hydrate skipped for %s/%s: %v", cap.ID, cap.OfferingID, err)
		}
	}
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
	cap.Extra["voices"] = map[string]any{
		"default": payload.DefaultVoice,
		"native":  payload.Voices.Native,
		"aliases": payload.Voices.Aliases,
	}
	return nil
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

type unexpectedStatusError struct {
	statusCode int
}

func (e *unexpectedStatusError) Error() string {
	return fmt.Sprintf("unexpected HTTP status %d", e.statusCode)
}
