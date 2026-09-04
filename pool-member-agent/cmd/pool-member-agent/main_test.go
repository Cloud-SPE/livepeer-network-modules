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
	if got.GPUUUID != "GPU-abc" || got.GPUModel != "NVIDIA GeForce RTX 4090" {
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

// No backend_ids on the attach path: their absence is what selects it
// on the broker side (runner-attach §2).
func TestWorkerSessionURL(t *testing.T) {
	got, err := workerSessionURL("https://broker.example")
	if err != nil {
		t.Fatalf("workerSessionURL() error = %v", err)
	}
	want := "wss://broker.example/internal/v1/worker/session"
	if got != want {
		t.Fatalf("workerSessionURL() = %q, want %q", got, want)
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
		gotTunnelHeader = r.Header.Get(LocalIDHeader)
		w.Header().Set("Livepeer-Work-Units", "7")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer runner.Close()

	resp := forwardTunnelRequest(t.Context(), map[string]string{"chat": runner.URL}, tunnelMessage{
		Type:       "request",
		ID:         "req-1",
		Method:     http.MethodPost,
		URL:        "http://worker.local/v1/chat?x=1",
		Headers:    map[string][]string{LocalIDHeader: {"chat"}},
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

// Routing is by local_id, because one host can serve the same
// capability id under two models (runner-attach §7).
func TestRouteFor(t *testing.T) {
	two := map[string]string{"chat-8b": "http://a", "chat-70b": "http://b"}
	one := map[string]string{"only": "http://solo"}

	if got, err := routeFor(two, map[string][]string{LocalIDHeader: {"chat-70b"}}); err != nil || got != "http://b" {
		t.Fatalf("routeFor(two, chat-70b) = %q, %v", got, err)
	}
	// A single runner still answers when the header is absent, so a
	// bare probe reaches it.
	// A derived id from a multi-capability container routes to the
	// container (attach.LocalIDFor / BaseLocalID).
	if got, err := routeFor(two, map[string][]string{LocalIDHeader: {"chat-70b.1"}}); err != nil || got != "http://b" {
		t.Fatalf("routeFor(two, chat-70b.1) = %q, %v", got, err)
	}
	if got, err := routeFor(one, nil); err != nil || got != "http://solo" {
		t.Fatalf("routeFor(one, no header) = %q, %v", got, err)
	}
	// Ambiguity is an error, never a guess.
	if _, err := routeFor(two, nil); err == nil {
		t.Fatal("routeFor(two, no header) picked a runner instead of failing")
	}
	if _, err := routeFor(two, map[string][]string{LocalIDHeader: {"nope"}}); err == nil {
		t.Fatal("routeFor accepted an unknown local_id")
	}
}

// The routing header is tunnel plumbing; the runner must not see it.
func TestRunnerHeadersStripsRoutingKeys(t *testing.T) {
	got := runnerHeaders(map[string][]string{
		LocalIDHeader:                  {"chat"},
		"X-Livepeer-Worker-Backend-Id": {"legacy"},
		"Content-Type":                 {"application/json"},
	})
	if got.Get(LocalIDHeader) != "" || got.Get("X-Livepeer-Worker-Backend-Id") != "" {
		t.Fatalf("routing headers survived: %v", got)
	}
	if got.Get("Content-Type") != "application/json" {
		t.Fatalf("application headers dropped: %v", got)
	}
}

func TestParseNVIDIASMIProducesAttachHardware(t *testing.T) {
	units, err := parseNVIDIASMI([]byte("GPU-abc, NVIDIA H100 80GB HBM3, 81559, 560.35.03\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 1 {
		t.Fatalf("units = %d", len(units))
	}
	u := units[0]
	if u.GPUUUID != "GPU-abc" || u.GPUModel != "NVIDIA H100 80GB HBM3" || u.Driver != "560.35.03" {
		t.Fatalf("unit = %+v", u)
	}
	if u.VRAMBytes != 81559*1024*1024 {
		t.Fatalf("vram = %d", u.VRAMBytes)
	}
}
