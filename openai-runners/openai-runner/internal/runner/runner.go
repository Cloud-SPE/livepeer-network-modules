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

type livepeerHeader struct {
	Request        string `json:"request"`
	Capability     string `json:"capability"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type Config struct {
	Endpoint     string
	Capability   string
	MaxBodyBytes int64
}

func Run(cfg Config) {
	addr := env("RUNNER_ADDR", ":8080")
	upstream := env("UPSTREAM_URL", "")
	if upstream == "" {
		log.Fatalf("UPSTREAM_URL is required, e.g. http://HOST:PORT%s", cfg.Endpoint)
	}

	transport := &http.Transport{
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
	client := &http.Client{Transport: transport}

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

	mux.HandleFunc(cfg.Endpoint, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if _, ok := loadModels(&discoveredModels); !ok {
			http.Error(w, "model not yet ready", http.StatusServiceUnavailable)
			return
		}

		ctx := r.Context()
		if lp, ok := decodeLivepeerHeader(r.Header.Get("Livepeer")); ok && lp.TimeoutSeconds > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(lp.TimeoutSeconds)*time.Second)
			defer cancel()
		}

		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, cfg.MaxBodyBytes))
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
		defer resp.Body.Close()

		copyAllHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		streamResponse(w, resp.Body)
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		models, ok := loadModels(&discoveredModels)
		if !ok {
			http.Error(w, "models not yet discovered", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"models": models,
		})
	})

	mux.HandleFunc("/"+cfg.Capability+"/options", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		models, _ := loadModels(&discoveredModels)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"models": models,
		})
	})

	log.Printf("openai-runner listening on %s endpoint=%s capability=%s upstream=%s",
		addr, cfg.Endpoint, cfg.Capability, upstream)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
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
	defer resp.Body.Close()
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

func streamResponse(w http.ResponseWriter, body io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := body.Read(buf)
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
