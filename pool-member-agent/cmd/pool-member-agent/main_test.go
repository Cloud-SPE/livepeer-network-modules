package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseNVIDIASMI(t *testing.T) {
	units, err := parseNVIDIASMI([]byte("GPU-abc, NVIDIA GeForce RTX 4090, 24564, 550.54\n"))
	if err != nil {
		t.Fatalf("parseNVIDIASMI() error = %v", err)
	}
	if len(units) != 1 {
		t.Fatalf("unit count = %d", len(units))
	}
	got := units[0]
	if got.ID != "gpu-gpu-abc" || got.GPUUUID != "GPU-abc" || got.GPUModel != "NVIDIA GeForce RTX 4090" {
		t.Fatalf("unit = %+v", got)
	}
	if got.VRAMBytes != 24564*1024*1024 {
		t.Fatalf("vram bytes = %d", got.VRAMBytes)
	}
}

func TestParseNVIDIASMIRejectsMalformedRows(t *testing.T) {
	if _, err := parseNVIDIASMI([]byte("GPU-abc, bad\n")); err == nil {
		t.Fatalf("parseNVIDIASMI() expected error")
	}
}

func TestWorkerSessionURL(t *testing.T) {
	got, err := workerSessionURL("https://broker.example", []string{"backend-a", "backend-b"})
	if err != nil {
		t.Fatalf("workerSessionURL() error = %v", err)
	}
	want := "wss://broker.example/internal/v1/worker/session?backend_ids=backend-a%2Cbackend-b"
	if got != want {
		t.Fatalf("workerSessionURL() = %q, want %q", got, want)
	}
}

func TestParseWorkerBackends(t *testing.T) {
	got := parseWorkerBackends("backend-a=http://127.0.0.1:9000, backend-b=http://runner:8080")
	if got["backend-a"] != "http://127.0.0.1:9000" || got["backend-b"] != "http://runner:8080" {
		t.Fatalf("parseWorkerBackends() = %#v", got)
	}
}

func TestJoinBackendURL(t *testing.T) {
	got, err := joinBackendURL("http://127.0.0.1:9000/base", "http://worker.local/v1/chat?x=1")
	if err != nil {
		t.Fatalf("joinBackendURL() error = %v", err)
	}
	want := "http://127.0.0.1:9000/base/v1/chat?x=1"
	if got != want {
		t.Fatalf("joinBackendURL() = %q, want %q", got, want)
	}
}

func TestForwardTunnelRequest(t *testing.T) {
	var gotPath, gotBody, gotTunnelHeader string
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		gotTunnelHeader = r.Header.Get("X-Livepeer-Worker-Backend-Id")
		w.Header().Set("Livepeer-Work-Units", "7")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer runner.Close()

	resp := forwardTunnelRequest(t.Context(), map[string]string{"backend-a": runner.URL}, tunnelMessage{
		Type:       "request",
		ID:         "req-1",
		Method:     http.MethodPost,
		URL:        "http://worker.local/v1/chat?x=1",
		Headers:    map[string][]string{"X-Livepeer-Worker-Backend-Id": []string{"backend-a"}},
		BodyBase64: base64Encode([]byte(`{"hello":"world"}`)),
	})
	if resp.Error != "" {
		t.Fatalf("forwardTunnelRequest() error = %s", resp.Error)
	}
	if resp.StatusCode != http.StatusAccepted || headerValue(resp.Headers, "Livepeer-Work-Units") != "7" {
		t.Fatalf("response = %+v", resp)
	}
	if gotPath != "/v1/chat?x=1" || gotBody != `{"hello":"world"}` || gotTunnelHeader != "" {
		t.Fatalf("runner got path=%q body=%q tunnelHeader=%q", gotPath, gotBody, gotTunnelHeader)
	}
	body, err := base64Decode(resp.BodyBase64)
	if err != nil {
		t.Fatalf("base64Decode() error = %v", err)
	}
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("body = %s", string(body))
	}
}
