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

func target(memberEthAddress, backendID, backendURL, capabilityID, interactionMode string) ProbeTarget {
	return ProbeTarget{
		Member: config.Member{
			EthAddress: memberEthAddress,
		},
		Backend: config.Backend{
			ID:        backendID,
			Transport: "http",
			URL:       backendURL,
		},
		Offering: config.Offering{
			CapabilityID:    capabilityID,
			OfferingID:      "default",
			InteractionMode: interactionMode,
		},
	}
}

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

	applied := make([]types.SyntheticProbeObservation, 0)
	runner := NewRunner(500 * time.Millisecond)
	summary, err := runner.RunOnceTargets(context.Background(), []ProbeTarget{
		target("0xabc", "backend-a", ts.URL+"/chat", "openai:chat-completions", "http-reqresp@v0"),
		target("0xabc", "backend-b", ts.URL+"/embeddings", "openai:embeddings", "http-reqresp@v0"),
		target("0xabc", "backend-c", ts.URL+"/audio-transcriptions", "openai:audio-transcriptions", "http-multipart@v0"),
	}, func(obs types.SyntheticProbeObservation) (types.BackendSelectionState, error) {
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

	applied := make([]types.SyntheticProbeObservation, 0, 2)
	runner := NewRunner(500 * time.Millisecond)
	summary, err := runner.RunOnceTargets(context.Background(), []ProbeTarget{
		target("0xabc", "backend-a", ts.URL+"/audio-generic-multipart", "openai:audio-unknown", "http-multipart@v0"),
		target("0xabc", "backend-b", ts.URL+"/audio-generic-speech", "openai:audio-generated", "http-reqresp@v0"),
	}, func(obs types.SyntheticProbeObservation) (types.BackendSelectionState, error) {
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
	applied := 0
	runner := NewRunner(500 * time.Millisecond)
	summary, err := runner.RunOnceTargets(context.Background(), []ProbeTarget{
		target("0xabc", "backend-a", "http://example.invalid/audio", "openai:audio-unknown", "http-stream@v0"),
	}, func(obs types.SyntheticProbeObservation) (types.BackendSelectionState, error) {
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

func TestRunOnceAppliesVideoABRProbe(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/video/transcode/abr/presets" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"presets": []map[string]any{
				{"name": "abr-standard"},
			},
		})
	}))
	defer ts.Close()

	applied := make([]types.SyntheticProbeObservation, 0, 1)
	runner := NewRunner(500 * time.Millisecond)
	summary, err := runner.RunOnceTargets(context.Background(), []ProbeTarget{
		target("0xabc", "backend-video", ts.URL+"/v1/video/transcode/abr", "video:transcode.abr", "http-reqresp@v0"),
	}, func(obs types.SyntheticProbeObservation) (types.BackendSelectionState, error) {
		applied = append(applied, obs)
		return types.BackendSelectionState{}, nil
	})
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if summary.Applied != 1 || summary.Succeeded != 1 || summary.Failed != 0 || summary.Skipped != 0 {
		t.Fatalf("summary = %#v", summary)
	}
	if len(applied) != 1 || !applied[0].Success {
		t.Fatalf("applied = %#v", applied)
	}
}

func TestRunOnceFailsVideoABRProbeOnInvalidPresetResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/video/transcode/abr/presets" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"presets": []map[string]any{},
		})
	}))
	defer ts.Close()

	applied := make([]types.SyntheticProbeObservation, 0, 1)
	runner := NewRunner(500 * time.Millisecond)
	summary, err := runner.RunOnceTargets(context.Background(), []ProbeTarget{
		target("0xabc", "backend-video", ts.URL, "video:transcode.abr", "http-reqresp@v0"),
	}, func(obs types.SyntheticProbeObservation) (types.BackendSelectionState, error) {
		applied = append(applied, obs)
		return types.BackendSelectionState{}, nil
	})
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if summary.Applied != 1 || summary.Succeeded != 0 || summary.Failed != 1 || summary.Skipped != 0 {
		t.Fatalf("summary = %#v", summary)
	}
	if len(applied) != 1 || applied[0].Success || applied[0].Result != "probe_invalid_response" {
		t.Fatalf("applied = %#v", applied)
	}
	if len(summary.Results) != 1 || summary.Results[0].Reason != "probe_invalid_response" {
		t.Fatalf("results = %#v", summary.Results)
	}
}
