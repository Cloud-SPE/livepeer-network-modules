// Package runner implements openai-embeddings-runner: a proxy that
// sits between the capability broker and a vLLM or Ollama embeddings
// backend.
//
// Unlike chat completions, OpenAI embeddings is a single
// request → single buffered JSON response — no streaming. So the
// runner:
//
//   - Reads the full upstream response body once.
//   - Parses `usage.total_tokens` (or the operator-configured field)
//     from the body and emits it as the X-Livepeer-Work-Units
//     **response header** (no trailer needed; headers are valid
//     because the body is non-streaming).
//   - Forwards the response body to the client byte-for-byte.
//
// The broker reads the header via its `response-header` extractor.
//
// Both vLLM-in-embed-mode and Ollama emit usage in the OpenAI-shaped
// embeddings response body, so a single code path covers both
// upstreams.
package runner

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

const (
	defaultEndpoint     = "/v1/embeddings"
	defaultCapability   = "openai-text-embeddings"
	defaultMaxBodyBytes = int64(1 << 20)
	workUnitsHeader     = "X-Livepeer-Work-Units"
)

// Run starts the runner with environment-driven config and blocks.
func Run() {
	addr := env("RUNNER_ADDR", ":8080")
	upstream := env("UPSTREAM_URL", "")
	if upstream == "" {
		log.Fatalf("UPSTREAM_URL is required, e.g. http://HOST:PORT%s", defaultEndpoint)
	}
	capability := env("CAPABILITY_NAME", defaultCapability)
	usageField := env("USAGE_FIELD", "total_tokens")
	upstreamKind := env("UPSTREAM_KIND", "vllm")
	maxBodyBytes := defaultMaxBodyBytes
	optionsCfg := optionsConfigFromEnv()
	optionsCfg.upstreamKind = upstreamKind

	client := &http.Client{Transport: newTransport()}

	var discoveredModels atomic.Value
	go func() {
		retries := envInt("MODEL_DISCOVERY_RETRIES", 10)
		ids, err := discoverModelsWithRetry(upstreamBase(upstream), retries, 10*time.Second)
		if err != nil {
			log.Fatalf("model discovery failed: %v", err)
		}
		discoveredModels.Store(ids)
		log.Printf("discovered %d model(s): %v", len(ids), ids)
	}()

	mux := http.NewServeMux()

	mux.HandleFunc(defaultEndpoint, func(w http.ResponseWriter, r *http.Request) {
		handleEmbeddings(w, r, client, upstream, maxBodyBytes, usageField, &discoveredModels)
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		models, ok := loadModels(&discoveredModels)
		if !ok {
			http.Error(w, "models not yet discovered", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "models": models})
	})

	mux.HandleFunc("/"+capability+"/options", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		models, _ := loadModels(&discoveredModels)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(buildOptionsPayload(models, optionsCfg))
	})

	log.Printf("openai-embeddings-runner listening on %s capability=%s upstream=%s upstream_kind=%s usage_field=%s",
		addr, capability, upstream, upstreamKind, usageField)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

func handleEmbeddings(w http.ResponseWriter, r *http.Request, client *http.Client, upstream string, maxBodyBytes int64, usageField string, discoveredModels *atomic.Value) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := loadModels(discoveredModels); !ok {
		http.Error(w, "model not yet ready", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	if lp, ok := decodeLivepeerHeader(r.Header.Get("Livepeer")); ok && lp.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(lp.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream, bytes.NewReader(bodyBytes))
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusBadGateway)
		return
	}
	req.ContentLength = int64(len(bodyBytes))
	copyHeader(req.Header, r.Header, []string{"Content-Type", "Accept"})
	req.Header.Del("Livepeer")
	req.Header.Del("Authorization")

	resp, err := client.Do(req)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "context deadline exceeded") {
			status = http.StatusGatewayTimeout
		}
		http.Error(w, "upstream request failed: "+err.Error(), status)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "failed to read upstream response: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Extract usage BEFORE writing headers so the work-units header can
	// be set. For embeddings the body is small and bounded; reading it
	// fully is fine.
	units := extractUsage(respBody, usageField)

	copyAllHeaders(w.Header(), resp.Header)
	if units > 0 {
		w.Header().Set(workUnitsHeader, fmt.Sprintf("%d", units))
	}
	w.Header().Del("Content-Length")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

type usageEnvelope struct {
	Usage *usageFields `json:"usage,omitempty"`
}

type usageFields struct {
	PromptTokens uint64 `json:"prompt_tokens"`
	TotalTokens  uint64 `json:"total_tokens"`
}

// extractUsage reads the OpenAI-shape `usage` block from an embeddings
// response body. Returns 0 if the body is empty, not JSON, or has no
// usage object. Mirrors the broker's openai-usage extractor logic so
// the runner-emitted header agrees with what direct-body extraction
// would have produced.
func extractUsage(body []byte, field string) uint64 {
	if len(bytes.TrimSpace(body)) == 0 {
		return 0
	}
	var env usageEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return 0
	}
	if env.Usage == nil {
		return 0
	}
	switch field {
	case "prompt_tokens":
		return env.Usage.PromptTokens
	default:
		return env.Usage.TotalTokens
	}
}

// ----- shared helpers (mirror chat-runner; both modules stay simple
// and don't share a Go package).

type livepeerHeader struct {
	Request        string `json:"request"`
	Capability     string `json:"capability"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func newTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          200,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

func loadModels(v *atomic.Value) ([]string, bool) {
	if loaded := v.Load(); loaded != nil {
		return loaded.([]string), true
	}
	return nil, false
}

func upstreamBase(upstream string) string {
	u, err := url.Parse(upstream)
	if err != nil {
		return upstream
	}
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	return u.String()
}

func discoverModelsWithRetry(base string, retries int, delay time.Duration) ([]string, error) {
	for i := 0; i < retries; i++ {
		if i > 0 {
			time.Sleep(delay)
		}
		ids, err := discoverModels(base)
		if err == nil {
			return ids, nil
		}
		log.Printf("model discovery attempt %d/%d failed: %v", i+1, retries, err)
	}
	return nil, fmt.Errorf("model discovery failed after %d attempts", retries)
}

func discoverModels(base string) ([]string, error) {
	resp, err := http.Get(base + "/v1/models")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from /v1/models", resp.StatusCode)
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode /v1/models response: %w", err)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no models returned from %s/v1/models", base)
	}
	ids := make([]string, len(result.Data))
	for i, m := range result.Data {
		ids[i] = m.ID
	}
	return ids, nil
}

func env(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}

func envInt(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return def
	}
	return n
}

func decodeLivepeerHeader(v string) (livepeerHeader, bool) {
	var lp livepeerHeader
	if v == "" {
		return lp, false
	}
	raw, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return lp, false
	}
	if err := json.Unmarshal(raw, &lp); err != nil {
		return lp, false
	}
	return lp, true
}

func copyHeader(dst http.Header, src http.Header, keys []string) {
	for _, k := range keys {
		if v := src.Get(k); v != "" {
			dst.Set(k, v)
		}
	}
}

func copyAllHeaders(dst http.Header, src http.Header) {
	for k, vv := range src {
		if strings.EqualFold(k, "Connection") ||
			strings.EqualFold(k, "Keep-Alive") ||
			strings.EqualFold(k, "Proxy-Authenticate") ||
			strings.EqualFold(k, "Proxy-Authorization") ||
			strings.EqualFold(k, "TE") ||
			strings.EqualFold(k, "Trailer") ||
			strings.EqualFold(k, "Transfer-Encoding") ||
			strings.EqualFold(k, "Upgrade") ||
			strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
