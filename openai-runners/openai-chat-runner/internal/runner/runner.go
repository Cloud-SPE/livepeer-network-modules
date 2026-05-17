// Package runner implements the openai-chat-runner: a proxy that sits
// between the capability broker and a vLLM (or OpenAI-compatible)
// chat-completions backend. Its added value over the transparent
// openai-runner is streaming-aware token counting:
//
//   - Streaming requests: the runner scans the upstream SSE response
//     for a final `usage` block (emitted by vLLM when
//     `stream_options.include_usage: true`), then declares
//     `X-Livepeer-Work-Units` as an HTTP trailer and emits the token
//     count after the body. The broker's response-trailer extractor
//     reads this trailer.
//
//   - Non-streaming requests: the runner passes the response through
//     unchanged. The broker uses its usual openai-usage extractor to
//     read `usage.total_tokens` from the JSON body.
//
// Auto-injects `stream_options.include_usage: true` on streaming
// requests when absent, so clients don't have to know about the
// billing requirement.
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
	defaultEndpoint     = "/v1/chat/completions"
	defaultCapability   = "openai-chat-completions"
	defaultMaxBodyBytes = int64(5 << 20)
	workUnitsTrailer    = "X-Livepeer-Work-Units"
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
		handleChatCompletions(w, r, client, upstream, maxBodyBytes, usageField, &discoveredModels)
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

	log.Printf("openai-chat-runner listening on %s capability=%s upstream=%s upstream_kind=%s usage_field=%s",
		addr, capability, upstream, upstreamKind, usageField)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request, client *http.Client, upstream string, maxBodyBytes int64, usageField string, discoveredModels *atomic.Value) {
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

	// Auto-inject include_usage on streaming requests. Non-streaming
	// requests pass through untouched.
	rewritten, isStream, err := ensureIncludeUsage(bodyBytes)
	if err != nil {
		http.Error(w, "request body is not valid JSON", http.StatusBadRequest)
		return
	}
	bodyBytes = rewritten

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

	if isStream && guardSSEContentType(resp.Header) == nil {
		writeStreamingResponse(w, resp, usageField)
		return
	}
	writePassThroughResponse(w, resp)
}

// writeStreamingResponse forwards an SSE response while counting
// tokens, then emits X-Livepeer-Work-Units as an HTTP trailer.
func writeStreamingResponse(w http.ResponseWriter, resp *http.Response, usageField string) {
	// Declare the trailer BEFORE WriteHeader; Go's server promotes
	// declared trailers to the wire trailer slot when set after the
	// body. Tracks the broker's http-stream driver pattern.
	w.Header().Set("Trailer", workUnitsTrailer)
	copyAllHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	total := streamAndCountUsage(w, resp.Body, usageField, flush)
	w.Header().Set(workUnitsTrailer, fmt.Sprintf("%d", total))
}

// writePassThroughResponse copies a non-streaming response (or an
// unexpected non-SSE response) to the client. Token counting is left
// to the broker's openai-usage extractor reading the body.
func writePassThroughResponse(w http.ResponseWriter, resp *http.Response) {
	copyAllHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// optionsConfig captures the operator-supplied metadata the runner
// surfaces via /<capability>/options. These map 1:1 to the fields the
// broker's chat-options discovery merges into the capability's `extra`
// block. Unset fields are simply omitted from the response so the
// broker can fall back to any host-config-declared values.
type optionsConfig struct {
	servedModelName string
	backendModel    string
	contextLength   int
	reasoningParser string
	toolCallParser  string
	quantization    string
	upstreamKind    string // "vllm" or "ollama"; advertised in payload
}

func optionsConfigFromEnv() optionsConfig {
	return optionsConfig{
		servedModelName: env("SERVED_MODEL_NAME", ""),
		backendModel:    env("BACKEND_MODEL", ""),
		contextLength:   envInt("CONTEXT_LENGTH", 0),
		reasoningParser: env("REASONING_PARSER", ""),
		toolCallParser:  env("TOOL_CALL_PARSER", ""),
		quantization:    env("QUANTIZATION", ""),
	}
}

// buildOptionsPayload returns the structured /<capability>/options
// payload the broker's chat-options discovery reads. Mirrors the shape
// of the audio/video options endpoints so the broker can hydrate the
// capability's `extra` block declaratively.
//
// The runner advertises:
//   - models / served_model_name — sourced from vLLM `/v1/models` and
//     the operator-supplied SERVED_MODEL_NAME (operator wins).
//   - backend_model — HuggingFace path or other upstream identifier.
//   - context_length — operator-declared max model length.
//   - parsers — operator-declared reasoning / tool-call parsers.
//   - quantization — operator-declared quantization scheme.
//   - features — derived booleans: streaming is always true (this
//     runner exists to count streaming tokens), include_usage_required
//     is always true (vLLM only emits usage when the flag is set;
//     this runner injects it for clients), tool_calling/reasoning are
//     derived from whether the operator declared a parser.
func buildOptionsPayload(models []string, cfg optionsConfig) map[string]any {
	out := map[string]any{
		"task":   "chat",
		"models": models,
	}
	if kind := strings.TrimSpace(cfg.upstreamKind); kind != "" {
		out["upstream_kind"] = kind
	}

	served := cfg.servedModelName
	if served == "" && len(models) > 0 {
		served = models[0]
	}
	if served != "" {
		out["served_model_name"] = served
	}
	if cfg.backendModel != "" {
		out["backend_model"] = cfg.backendModel
	}
	if cfg.contextLength > 0 {
		out["context_length"] = cfg.contextLength
	}
	if cfg.quantization != "" {
		out["quantization"] = cfg.quantization
	}

	parsers := map[string]any{}
	if cfg.reasoningParser != "" {
		parsers["reasoning"] = cfg.reasoningParser
	}
	if cfg.toolCallParser != "" {
		parsers["tool_call"] = cfg.toolCallParser
	}
	if len(parsers) > 0 {
		out["parsers"] = parsers
	}

	features := map[string]any{
		"streaming":              true,
		"include_usage_required": true,
	}
	if cfg.reasoningParser != "" {
		features["reasoning"] = true
	}
	if cfg.toolCallParser != "" {
		features["tool_calling"] = true
	}
	out["features"] = features

	return out
}

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
			strings.EqualFold(k, "Upgrade") {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
