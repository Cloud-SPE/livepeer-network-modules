package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go"
)

type config struct {
	ControllerURL     string
	BrokerURL         string
	BrokerQUICAddr    string
	EnrollmentID      string
	TokenFile         string
	SessionCredential string
	WorkerBackends    map[string]string
	ReportEvery       time.Duration
}

type hardwareReport struct {
	HardwareUnits []hardwareUnit `json:"hardware_units"`
}

type hardwareUnit struct {
	ID            string            `json:"id,omitempty"`
	GPUUUID       string            `json:"gpu_uuid"`
	GPUModel      string            `json:"gpu_model"`
	VRAMBytes     uint64            `json:"vram_bytes,omitempty"`
	DriverVersion string            `json:"driver_version,omitempty"`
	RuntimeFacts  map[string]string `json:"runtime_facts,omitempty"`
}

type tunnelMessage struct {
	Type       string              `json:"type"`
	ID         string              `json:"id"`
	Method     string              `json:"method,omitempty"`
	URL        string              `json:"url,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	BodyBase64 string              `json:"body_base64,omitempty"`
	StatusCode int                 `json:"status_code,omitempty"`
	Error      string              `json:"error,omitempty"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string) error {
	cfg := loadConfig(args)
	if cfg.ControllerURL == "" || cfg.EnrollmentID == "" || cfg.TokenFile == "" {
		return errors.New("POOL_CONTROLLER_URL, POOL_ENROLLMENT_ID, and POOL_ENROLLMENT_TOKEN_FILE are required")
	}
	tokenBytes, err := os.ReadFile(cfg.TokenFile)
	if err != nil {
		return fmt.Errorf("read enrollment token: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return errors.New("enrollment token file is empty")
	}
	if cfg.ReportEvery <= 0 {
		cfg.ReportEvery = time.Minute
	}
	errCh := make(chan error, 2)
	go func() { errCh <- reportLoop(ctx, cfg, token) }()
	if cfg.BrokerURL != "" && len(cfg.WorkerBackends) > 0 {
		go func() { errCh <- tunnelLoop(ctx, cfg) }()
	}
	err = <-errCh
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func loadConfig(args []string) config {
	fs := flag.NewFlagSet("pool-member-agent", flag.ExitOnError)
	reportEvery := fs.Duration("report-every", envDuration("POOL_REPORT_EVERY", time.Minute), "hardware report interval")
	_ = fs.Parse(args)
	return config{
		ControllerURL:     strings.TrimRight(os.Getenv("POOL_CONTROLLER_URL"), "/"),
		BrokerURL:         strings.TrimRight(os.Getenv("POOL_BROKER_URL"), "/"),
		BrokerQUICAddr:    strings.TrimSpace(os.Getenv("POOL_BROKER_QUIC_ADDR")),
		EnrollmentID:      strings.TrimSpace(os.Getenv("POOL_ENROLLMENT_ID")),
		TokenFile:         strings.TrimSpace(os.Getenv("POOL_ENROLLMENT_TOKEN_FILE")),
		SessionCredential: strings.TrimSpace(os.Getenv("POOL_BROKER_SESSION_CREDENTIAL")),
		WorkerBackends:    parseWorkerBackends(os.Getenv("POOL_WORKER_BACKENDS")),
		ReportEvery:       *reportEvery,
	}
}

func reportLoop(ctx context.Context, cfg config, token string) error {
	for {
		if err := reportOnce(ctx, cfg, token); err != nil {
			log.Printf("hardware report failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(cfg.ReportEvery):
		}
	}
}

func tunnelLoop(ctx context.Context, cfg config) error {
	backoff := time.Second
	for {
		if err := runTunnel(ctx, cfg); err != nil {
			log.Printf("worker tunnel disconnected: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func runTunnel(ctx context.Context, cfg config) error {
	if cfg.BrokerQUICAddr != "" {
		return runQUICTunnel(ctx, cfg)
	}
	sessionURL, err := workerSessionURL(cfg.BrokerURL, backendIDs(cfg.WorkerBackends))
	if err != nil {
		return err
	}
	headers := http.Header{}
	if cfg.SessionCredential != "" {
		headers.Set("Authorization", "Bearer "+cfg.SessionCredential)
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, sessionURL, headers)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	log.Printf("worker tunnel connected backends=%s", strings.Join(backendIDs(cfg.WorkerBackends), ","))
	for {
		var msg tunnelMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return err
		}
		if msg.Type != "request" {
			continue
		}
		go handleTunnelRequest(ctx, conn, cfg.WorkerBackends, msg)
	}
}

func runQUICTunnel(ctx context.Context, cfg config) error {
	conn, err := quic.DialAddr(ctx, cfg.BrokerQUICAddr, quicClientTLSConfig(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = conn.CloseWithError(0, "closed") }()
	if err := quicRegister(ctx, conn, cfg); err != nil {
		return err
	}
	log.Printf("worker quic tunnel connected addr=%s backends=%s", cfg.BrokerQUICAddr, strings.Join(backendIDs(cfg.WorkerBackends), ","))
	for {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			return err
		}
		go handleQUICRequest(ctx, stream, cfg.WorkerBackends)
	}
}

func quicRegister(ctx context.Context, conn *quic.Conn, cfg config) error {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	headers := map[string][]string{
		"Backend-Ids": []string{strings.Join(backendIDs(cfg.WorkerBackends), ",")},
	}
	if cfg.SessionCredential != "" {
		headers["Authorization"] = []string{"Bearer " + cfg.SessionCredential}
	}
	msg := tunnelMessage{Type: "register", ID: "register", Headers: headers}
	if err := json.NewEncoder(stream).Encode(msg); err != nil {
		_ = stream.Close()
		return err
	}
	if err := stream.Close(); err != nil {
		return err
	}
	var resp tunnelMessage
	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		return err
	}
	if resp.Error != "" || resp.StatusCode >= 400 {
		return fmt.Errorf("quic register failed: %s", resp.Error)
	}
	return nil
}

func quicClientTLSConfig() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"livepeer-pool-worker/1"}}
}

func writeQUICFrameHeader(w io.Writer, msg tunnelMessage) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(raw) > 16*1024*1024 {
		return fmt.Errorf("quic frame header too large")
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(raw)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func readQUICFrameHeader(r io.Reader) (tunnelMessage, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return tunnelMessage{}, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || n > 16*1024*1024 {
		return tunnelMessage{}, fmt.Errorf("invalid quic frame header length %d", n)
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(r, raw); err != nil {
		return tunnelMessage{}, err
	}
	var msg tunnelMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return tunnelMessage{}, err
	}
	return msg, nil
}

func handleQUICRequest(ctx context.Context, stream *quic.Stream, backends map[string]string) {
	defer func() { _ = stream.Close() }()
	msg, err := readQUICFrameHeader(stream)
	if err != nil {
		_ = writeQUICFrameHeader(stream, tunnelMessage{Type: "response", Error: err.Error()})
		return
	}
	if msg.Type != "request" {
		_ = writeQUICFrameHeader(stream, tunnelMessage{Type: "response", ID: msg.ID, Error: "expected request"})
		return
	}
	if err := forwardQUICRequest(ctx, stream, backends, msg); err != nil {
		log.Printf("worker quic response write failed: %v", err)
	}
}

func forwardQUICRequest(ctx context.Context, stream *quic.Stream, backends map[string]string, msg tunnelMessage) error {
	backendID := headerValue(msg.Headers, "X-Livepeer-Worker-Backend-Id")
	base := backends[backendID]
	if base == "" && len(backends) == 1 {
		for _, only := range backends {
			base = only
		}
	}
	if base == "" {
		return writeQUICFrameHeader(stream, tunnelMessage{Type: "response", ID: msg.ID, Error: "unknown backend id " + backendID})
	}
	target, err := joinBackendURL(base, msg.URL)
	if err != nil {
		return writeQUICFrameHeader(stream, tunnelMessage{Type: "response", ID: msg.ID, Error: err.Error()})
	}
	req, err := http.NewRequestWithContext(ctx, defaultMethod(msg.Method), target, stream)
	if err != nil {
		return writeQUICFrameHeader(stream, tunnelMessage{Type: "response", ID: msg.ID, Error: err.Error()})
	}
	req.Header = http.Header(msg.Headers).Clone()
	req.Header.Del("X-Livepeer-Worker-Backend-Id")
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return writeQUICFrameHeader(stream, tunnelMessage{Type: "response", ID: msg.ID, Error: err.Error()})
	}
	defer func() { _ = httpResp.Body.Close() }()
	if err := writeQUICFrameHeader(stream, tunnelMessage{
		Type:       "response",
		ID:         msg.ID,
		StatusCode: httpResp.StatusCode,
		Headers:    map[string][]string(httpResp.Header),
	}); err != nil {
		return err
	}
	_, err = io.Copy(stream, httpResp.Body)
	return err
}

func handleTunnelRequest(ctx context.Context, conn *websocket.Conn, backends map[string]string, msg tunnelMessage) {
	resp := forwardTunnelRequest(ctx, backends, msg)
	writeTunnelResponse(conn, resp)
}

func forwardTunnelRequest(ctx context.Context, backends map[string]string, msg tunnelMessage) tunnelMessage {
	resp := tunnelMessage{Type: "response", ID: msg.ID}
	backendID := headerValue(msg.Headers, "X-Livepeer-Worker-Backend-Id")
	base := backends[backendID]
	if base == "" && len(backends) == 1 {
		for _, only := range backends {
			base = only
		}
	}
	if base == "" {
		resp.Error = "unknown backend id " + backendID
		return resp
	}
	target, err := joinBackendURL(base, msg.URL)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	body, err := base64Decode(msg.BodyBase64)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	req, err := http.NewRequestWithContext(ctx, defaultMethod(msg.Method), target, bytes.NewReader(body))
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	req.Header = http.Header(msg.Headers).Clone()
	req.Header.Del("X-Livepeer-Worker-Backend-Id")
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	defer func() { _ = httpResp.Body.Close() }()
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	resp.StatusCode = httpResp.StatusCode
	resp.Headers = map[string][]string(httpResp.Header)
	resp.BodyBase64 = base64Encode(respBody)
	return resp
}

var tunnelWriteMu sync.Mutex

func writeTunnelResponse(conn *websocket.Conn, msg tunnelMessage) {
	tunnelWriteMu.Lock()
	defer tunnelWriteMu.Unlock()
	if err := conn.WriteJSON(msg); err != nil {
		log.Printf("worker tunnel response write failed: %v", err)
	}
}

func reportOnce(ctx context.Context, cfg config, token string) error {
	units, err := collectNVIDIAGPUs(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(hardwareReport{HardwareUnits: units})
	if err != nil {
		return err
	}
	url := cfg.ControllerURL + "/member/v1/enrollments/" + cfg.EnrollmentID + "/hardware"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("controller returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	log.Printf("reported %d hardware units", len(units))
	return nil
}

func collectNVIDIAGPUs(ctx context.Context) ([]hardwareUnit, error) {
	cmd := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=uuid,name,memory.total,driver_version", "--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi query failed: %w", err)
	}
	return parseNVIDIASMI(out)
}

func parseWorkerBackends(raw string) map[string]string {
	out := make(map[string]string)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimRight(strings.TrimSpace(value), "/")
		if key != "" && value != "" {
			out[key] = value
		}
	}
	return out
}

func backendIDs(backends map[string]string) []string {
	ids := make([]string, 0, len(backends))
	for id := range backends {
		ids = append(ids, id)
	}
	return ids
}

func workerSessionURL(rawBrokerURL string, ids []string) (string, error) {
	if rawBrokerURL == "" {
		return "", errors.New("POOL_BROKER_URL is required when POOL_WORKER_BACKENDS is set")
	}
	u, err := url.Parse(rawBrokerURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported broker url scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/internal/v1/worker/session"
	q := u.Query()
	q.Set("backend_ids", strings.Join(ids, ","))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func joinBackendURL(baseURL, tunnelURL string) (string, error) {
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return "", err
	}
	tunnel, err := url.Parse(tunnelURL)
	if err != nil {
		return "", err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(tunnel.Path, "/")
	base.RawQuery = tunnel.RawQuery
	return base.String(), nil
}

func headerValue(headers map[string][]string, key string) string {
	for gotKey, values := range headers {
		if strings.EqualFold(gotKey, key) && len(values) > 0 {
			return strings.TrimSpace(values[0])
		}
	}
	return ""
}

func defaultMethod(method string) string {
	if strings.TrimSpace(method) == "" {
		return http.MethodPost
	}
	return method
}

func base64Encode(body []byte) string {
	return base64.StdEncoding.EncodeToString(body)
}

func base64Decode(raw string) ([]byte, error) {
	if raw == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(raw)
}

func parseNVIDIASMI(out []byte) ([]hardwareUnit, error) {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	units := make([]hardwareUnit, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 4 {
			return nil, fmt.Errorf("unexpected nvidia-smi row %q", line)
		}
		uuid := strings.TrimSpace(parts[0])
		name := strings.TrimSpace(parts[1])
		memoryMiB, err := strconv.ParseUint(strings.TrimSpace(parts[2]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse gpu memory for %s: %w", uuid, err)
		}
		driver := strings.TrimSpace(parts[3])
		units = append(units, hardwareUnit{
			ID:            "gpu-" + sanitizeID(uuid),
			GPUUUID:       uuid,
			GPUModel:      name,
			VRAMBytes:     memoryMiB * 1024 * 1024,
			DriverVersion: driver,
			RuntimeFacts:  map[string]string{"source": "nvidia-smi"},
		})
	}
	return units, nil
}

func sanitizeID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	replacer := strings.NewReplacer(" ", "-", "_", "-", "/", "-", ":", "-")
	return replacer.Replace(raw)
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}
