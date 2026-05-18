package probes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestRunOnceAppliesChatEmbeddingsAndAudioProbes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/chat":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"index": 0}},
			})
		case "/embeddings":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"embedding": []float64{0.1, 0.2}}},
			})
		case "/audio-transcriptions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"text": "ping",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	cfg := &config.Config{
		Members: []config.Member{
			{
				EthAddress: "0xabc",
				Backends: []config.Backend{
					{
						ID:        "backend-a",
						Transport: "http",
						URL:       ts.URL + "/chat",
							Offerings: []config.Offering{{
								CapabilityID: "openai:chat-completions",
								OfferingID:   "default",
								InteractionMode: "http-reqresp@v0",
							}},
						},
					{
						ID:        "backend-b",
						Transport: "http",
						URL:       ts.URL + "/embeddings",
							Offerings: []config.Offering{{
								CapabilityID: "openai:embeddings",
								OfferingID:   "default",
								InteractionMode: "http-reqresp@v0",
							}},
						},
					{
						ID:        "backend-c",
						Transport: "http",
						URL:       ts.URL + "/audio-transcriptions",
							Offerings: []config.Offering{{
								CapabilityID: "openai:audio-transcriptions",
								OfferingID:   "default",
								InteractionMode: "http-multipart@v0",
							}},
						},
				},
			},
		},
	}

	applied := make([]types.SyntheticProbeObservation, 0)
	runner := NewRunner(500 * time.Millisecond)
	summary, err := runner.RunOnce(context.Background(), cfg, func(obs types.SyntheticProbeObservation) (types.BackendSelectionState, error) {
		applied = append(applied, obs)
		return types.BackendSelectionState{}, nil
	})
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if summary.Applied != 3 || summary.Succeeded != 3 || summary.Failed != 0 || summary.Skipped != 0 {
		t.Fatalf("summary = %#v", summary)
	}
	if len(applied) != 3 {
		t.Fatalf("len(applied) = %d, want 3", len(applied))
	}
	for _, obs := range applied {
		if !obs.Success {
			t.Fatalf("observation = %#v, want success", obs)
		}
	}
}

func TestRunOnceInfersAudioProbeFamilyFromInteractionMode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/audio-generic-multipart":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"text": "ping",
			})
		case "/audio-generic-speech":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	cfg := &config.Config{
		Members: []config.Member{
			{
				EthAddress: "0xabc",
				Backends: []config.Backend{
					{
						ID:        "backend-a",
						Transport: "http",
						URL:       ts.URL + "/audio-generic-multipart",
						Offerings: []config.Offering{{
							CapabilityID:    "openai:audio-unknown",
							OfferingID:      "default",
							InteractionMode: "http-multipart@v0",
						}},
					},
					{
						ID:        "backend-b",
						Transport: "http",
						URL:       ts.URL + "/audio-generic-speech",
						Offerings: []config.Offering{{
							CapabilityID:    "openai:audio-generated",
							OfferingID:      "default",
							InteractionMode: "http-reqresp@v0",
						}},
					},
				},
			},
		},
	}

	applied := make([]types.SyntheticProbeObservation, 0, 2)
	runner := NewRunner(500 * time.Millisecond)
	summary, err := runner.RunOnce(context.Background(), cfg, func(obs types.SyntheticProbeObservation) (types.BackendSelectionState, error) {
		applied = append(applied, obs)
		return types.BackendSelectionState{}, nil
	})
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if summary.Applied != 2 || summary.Succeeded != 2 || summary.Failed != 0 || summary.Skipped != 0 {
		t.Fatalf("summary = %#v", summary)
	}
	if len(applied) != 2 {
		t.Fatalf("len(applied) = %d, want 2", len(applied))
	}
}

func TestRunOnceSkipsUnsupportedAudioFamily(t *testing.T) {
	cfg := &config.Config{
		Members: []config.Member{
			{
				EthAddress: "0xabc",
				Backends: []config.Backend{
					{
						ID:        "backend-a",
						Transport: "http",
						URL:       "http://example.invalid/audio",
						Offerings: []config.Offering{{
							CapabilityID:    "openai:audio-unknown",
							OfferingID:      "default",
							InteractionMode: "http-stream@v0",
						}},
					},
				},
			},
		},
	}

	applied := 0
	runner := NewRunner(500 * time.Millisecond)
	summary, err := runner.RunOnce(context.Background(), cfg, func(obs types.SyntheticProbeObservation) (types.BackendSelectionState, error) {
		applied++
		return types.BackendSelectionState{}, nil
	})
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if summary.Skipped != 1 || summary.Applied != 0 {
		t.Fatalf("summary = %#v", summary)
	}
	if applied != 0 {
		t.Fatalf("applied callback count = %d, want 0", applied)
	}
	if len(summary.Results) != 1 || summary.Results[0].Reason != "audio_probe_not_implemented" {
		t.Fatalf("results = %#v", summary.Results)
	}
}
