package main

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	transcode "github.com/Cloud-SPE/livepeer-network-rewrite/video-runners/transcode-core"
)

//go:embed presets.yaml
var defaultPresetsYAML []byte

// ── Configuration ──

var (
	runnerAddr   = env("RUNNER_ADDR", ":8080")
	maxQueueSize = envInt("MAX_QUEUE_SIZE", 5)
	tempDir      = env("TEMP_DIR", "/tmp/transcode")
	jobTTL       = time.Duration(envInt("JOB_TTL_SECONDS", 3600)) * time.Second
)

// ── Global state ──

var (
	jobs       = make(map[string]*Job)
	jobsMu     sync.RWMutex
	activeJobs atomic.Int32
	hw         transcode.HWProfile
	presets    []transcode.Preset
)

// ── Request / Response types ──

type TranscodeRequest struct {
	InputURL        string  `json:"input_url"`
	OutputURL       string  `json:"output_url"`
	Preset          string  `json:"preset"`
	WebhookURL      string  `json:"webhook_url,omitempty"`
	WebhookSecret   string  `json:"webhook_secret,omitempty"`
	SubtitleURL     string  `json:"subtitle_url,omitempty"`
	WatermarkURL    string  `json:"watermark_url,omitempty"`
	WatermarkPos    string  `json:"watermark_position,omitempty"`
	WatermarkScale  float64 `json:"watermark_scale,omitempty"`
	ThumbnailURL    string  `json:"thumbnail_url,omitempty"`
	ThumbnailSeek   float64 `json:"thumbnail_seek,omitempty"`
	ThumbnailWidth  int     `json:"thumbnail_width,omitempty"`
	ThumbnailHeight int     `json:"thumbnail_height,omitempty"`
	ToneMap         bool    `json:"tone_map,omitempty"`
}

type JobStatusResponse struct {
	JobID          string      `json:"job_id"`
	Status         string      `json:"status"`
	Phase          string      `json:"phase"`
	Progress       float64     `json:"progress"`
	EncodingFPS    float64     `json:"encoding_fps,omitempty"`
	Speed          string      `json:"speed,omitempty"`
	ETA            int         `json:"eta_seconds,omitempty"`
	InputInfo      *ProbeInfo  `json:"input,omitempty"`
	OutputInfo     *OutputInfo `json:"output,omitempty"`
	Error          string      `json:"error,omitempty"`
	ErrorCode      string      `json:"error_code,omitempty"`
	ProcessingTime float64     `json:"processing_time_seconds,omitempty"`
	GPU            string      `json:"gpu,omitempty"`
	CreatedAt      string      `json:"created_at"`
	CompletedAt    string      `json:"completed_at,omitempty"`
}

type ProbeInfo struct {
	Duration   float64 `json:"duration"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	VideoCodec string  `json:"video_codec"`
	AudioCodec string  `json:"audio_codec"`
	FPS        float64 `json:"fps"`
	Bitrate    int     `json:"bitrate"`
	PixFmt     string  `json:"pix_fmt"`
}

type OutputInfo struct {
	Duration   float64 `json:"duration"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	VideoCodec string  `json:"video_codec"`
	AudioCodec string  `json:"audio_codec"`
	FPS        float64 `json:"fps"`
	Bitrate    int     `json:"bitrate"`
	FileSize   int64   `json:"file_size"`
}

type StatusRequest struct {
	JobID string `json:"job_id"`
}

// ── Job ──

type Job struct {
	mu          sync.Mutex
	ID          string
	Status      string // queued, probing, downloading, encoding, uploading, complete, error
	Phase       string
	Progress    float64
	EncodingFPS float64
	Speed       float64
	InputInfo   *ProbeInfo
	OutputInfo  *OutputInfo
	Error       string
	ErrorCode   string
	Request     TranscodeRequest
	CreatedAt   time.Time
	CompletedAt time.Time
	StartedAt   time.Time
}

func (j *Job) setPhase(phase string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = phase
	j.Phase = phase
	log.Printf("[job %s] phase: %s", j.ID, phase)
}

func (j *Job) setProgress(pct float64, info transcode.ProgressInfo) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Progress = pct
	j.EncodingFPS = info.FPS
	j.Speed = info.Speed
}

func (j *Job) setInputInfo(probe transcode.ProbeResult) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.InputInfo = &ProbeInfo{
		Duration:   probe.Duration,
		Width:      probe.Width,
		Height:     probe.Height,
		VideoCodec: probe.VideoCodec,
		AudioCodec: probe.AudioCodec,
		FPS:        probe.FPS,
		Bitrate:    probe.Bitrate,
		PixFmt:     probe.PixFmt,
	}
}

func (j *Job) setOutputInfo(probe transcode.ProbeResult, fileSize int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.OutputInfo = &OutputInfo{
		Duration:   probe.Duration,
		Width:      probe.Width,
		Height:     probe.Height,
		VideoCodec: probe.VideoCodec,
		AudioCodec: probe.AudioCodec,
		FPS:        probe.FPS,
		Bitrate:    probe.Bitrate,
		FileSize:   fileSize,
	}
}

func (j *Job) setComplete() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = "complete"
	j.Phase = "complete"
	j.Progress = 100.0
	j.CompletedAt = time.Now()
	log.Printf("[job %s] complete (%.1fs)", j.ID, j.CompletedAt.Sub(j.StartedAt).Seconds())
}

func (j *Job) setError(err error, code string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = "error"
	j.Phase = "error"
	j.Error = err.Error()
	j.ErrorCode = code
	j.CompletedAt = time.Now()
	log.Printf("[job %s] error (%s): %v", j.ID, code, err)
}

func (j *Job) toResponse() JobStatusResponse {
	j.mu.Lock()
	defer j.mu.Unlock()

	resp := JobStatusResponse{
		JobID:       j.ID,
		Status:      j.Status,
		Phase:       j.Phase,
		Progress:    j.Progress,
		EncodingFPS: j.EncodingFPS,
		GPU:         hw.GPUName,
		CreatedAt:   j.CreatedAt.UTC().Format(time.RFC3339),
	}

	if j.Speed > 0 {
		resp.Speed = fmt.Sprintf("%.2fx", j.Speed)
	}

	if j.InputInfo != nil && j.Speed > 0 && j.Progress < 100 {
		remaining := (100.0 - j.Progress) / 100.0 * j.InputInfo.Duration
		if j.Speed > 0 {
			resp.ETA = int(remaining / j.Speed)
		}
	}

	if j.InputInfo != nil {
		resp.InputInfo = j.InputInfo
	}
	if j.OutputInfo != nil {
		resp.OutputInfo = j.OutputInfo
	}
	if j.Error != "" {
		resp.Error = j.Error
		resp.ErrorCode = j.ErrorCode
	}
	if !j.CompletedAt.IsZero() {
		resp.CompletedAt = j.CompletedAt.UTC().Format(time.RFC3339)
		resp.ProcessingTime = j.CompletedAt.Sub(j.StartedAt).Seconds()
	}

	return resp
}

// ── HTTP Handlers ──

func handleSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20)) // 5MB limit
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
		return
	}

	var req TranscodeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	// Validate required fields
	if req.InputURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "input_url is required"})
		return
	}
	if req.OutputURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "output_url is required"})
		return
	}
	if req.Preset == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "preset is required"})
		return
	}

	// Validate preset exists
	if _, ok := transcode.FindPreset(presets, req.Preset); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":             fmt.Sprintf("unknown preset: %s", req.Preset),
			"available_presets": strings.Join(presetNames(), ", "),
		})
		return
	}

	// Check capacity
	current := int(activeJobs.Load())
	if current >= maxQueueSize {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error":       "server at capacity",
			"active_jobs": strconv.Itoa(current),
			"max_jobs":    strconv.Itoa(maxQueueSize),
		})
		return
	}

	// Create job
	jobID := generateJobID()
	job := &Job{
		ID:        jobID,
		Status:    "queued",
		Phase:     "queued",
		Request:   req,
		CreatedAt: time.Now(),
		StartedAt: time.Now(),
	}

	jobsMu.Lock()
	jobs[jobID] = job
	jobsMu.Unlock()

	activeJobs.Add(1)
	go runJob(job)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"job_id": jobID,
		"status": "queued",
	})
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
		return
	}

	var req StatusRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if req.JobID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "job_id is required"})
		return
	}

	jobsMu.RLock()
	job, ok := jobs[req.JobID]
	jobsMu.RUnlock()

	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}

	writeJSON(w, http.StatusOK, job.toResponse())
}

func handlePresets(w http.ResponseWriter, r *http.Request) {
	type presetInfo struct {
		Presets []transcode.Preset `json:"presets"`
		GPU     string             `json:"gpu"`
		Count   int                `json:"count"`
	}
	writeJSON(w, http.StatusOK, presetInfo{
		Presets: presets,
		GPU:     hw.GPUName,
		Count:   len(presets),
	})
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":      "ok",
		"gpu":         hw.GPUName,
		"vram_mb":     hw.VRAM_MB,
		"active_jobs": activeJobs.Load(),
		"max_jobs":    maxQueueSize,
		"presets":     len(presets),
	})
}

// ── Job Worker ──

func runJob(job *Job) {
	preset, _ := transcode.FindPreset(presets, job.Request.Preset)

	// Create per-job temp directory
	jobTempDir := filepath.Join(tempDir, job.ID)
	if err := os.MkdirAll(jobTempDir, 0755); err != nil {
		job.setError(fmt.Errorf("create temp dir: %w", err), "TEMP_DIR_ERROR")
		activeJobs.Add(-1)
		sendWebhook(job, "job.error")
		return
	}
	defer func() {
		os.RemoveAll(jobTempDir)
		activeJobs.Add(-1)
	}()

	inputPath := filepath.Join(jobTempDir, "input"+guessExtension(job.Request.InputURL))
	outputPath := filepath.Join(jobTempDir, "output.mp4")

	// Phase 1: Download input
	job.setPhase("downloading")
	ctx := context.Background()
	_, err := transcode.DownloadFile(ctx, job.Request.InputURL, inputPath, func(transferred, total int64) {
		if total > 0 {
			pct := float64(transferred) / float64(total) * 100.0
			job.mu.Lock()
			job.Progress = pct * 0.08 // download is 0-8% of total progress
			job.mu.Unlock()
		}
	})
	if err != nil {
		job.setError(fmt.Errorf("download input: %w", err), "DOWNLOAD_ERROR")
		sendWebhook(job, "job.error")
		return
	}

	// Download subtitle file if provided
	var subtitlePath string
	if job.Request.SubtitleURL != "" {
		subtitlePath = filepath.Join(jobTempDir, "subtitles"+guessExtension(job.Request.SubtitleURL))
		if _, err := transcode.DownloadFile(ctx, job.Request.SubtitleURL, subtitlePath, nil); err != nil {
			job.setError(fmt.Errorf("download subtitle: %w", err), "DOWNLOAD_ERROR")
			sendWebhook(job, "job.error")
			return
		}
	}

	// Download watermark image if provided
	var watermarkPath string
	if job.Request.WatermarkURL != "" {
		watermarkPath = filepath.Join(jobTempDir, "watermark"+guessExtension(job.Request.WatermarkURL))
		if _, err := transcode.DownloadFile(ctx, job.Request.WatermarkURL, watermarkPath, nil); err != nil {
			job.setError(fmt.Errorf("download watermark: %w", err), "DOWNLOAD_ERROR")
			sendWebhook(job, "job.error")
			return
		}
	}

	// Phase 2: Probe
	job.setPhase("probing")
	probeCmd := transcode.ProbeCmd(inputPath)
	probeOutput, err := probeCmd.Output()
	if err != nil {
		job.setError(fmt.Errorf("probe input: %w", err), "PROBE_ERROR")
		sendWebhook(job, "job.error")
		return
	}

	probe, err := transcode.ParseProbeOutput(probeOutput)
	if err != nil {
		job.setError(fmt.Errorf("parse probe output: %w", err), "PROBE_ERROR")
		sendWebhook(job, "job.error")
		return
	}
	job.setInputInfo(probe)
	sendWebhook(job, "job.probed")

	totalDuration, _ := transcode.ParseDuration(
		fmt.Sprintf("%02d:%02d:%06.3f",
			int(probe.Duration)/3600,
			(int(probe.Duration)%3600)/60,
			probe.Duration-float64(int(probe.Duration)/60*60)),
	)
	// Simpler: use probe.Duration directly as seconds
	totalDur := time.Duration(probe.Duration * float64(time.Second))

	// Build TranscodeOptions from request
	opts := transcode.TranscodeOptions{
		SubtitlePath:   subtitlePath,
		WatermarkPath:  watermarkPath,
		WatermarkPos:   job.Request.WatermarkPos,
		WatermarkScale: job.Request.WatermarkScale,
		ToneMap:        job.Request.ToneMap,
	}

	// Auto-detect HDR: if input is HDR and tone_map not explicitly set, enable it
	if probe.IsHDR() && !job.Request.ToneMap {
		opts.ToneMap = true
		log.Printf("[job %s] auto-detected HDR input (%s), enabling tone mapping", job.ID, probe.ColorTransfer)
	}

	// Phase 3: Encode
	job.setPhase("encoding")
	sendWebhook(job, "job.encoding")

	cmd := transcode.TranscodeCmd(inputPath, outputPath, preset, hw, probe, opts)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		job.setError(fmt.Errorf("create stderr pipe: %w", err), "ENCODE_ERROR")
		sendWebhook(job, "job.error")
		return
	}

	if err := cmd.Start(); err != nil {
		job.setError(fmt.Errorf("start ffmpeg: %w", err), "ENCODE_ERROR")
		sendWebhook(job, "job.error")
		return
	}

	// Parse progress from ffmpeg stderr
	scanner := bufio.NewScanner(stderr)
	scanner.Split(scanFFmpegLines)
	for scanner.Scan() {
		line := scanner.Text()
		if info, ok := transcode.ParseProgressLine(line); ok {
			pct := transcode.CalcPercent(info.Time, totalDur)
			// Encoding is 10-90% of total progress
			adjustedPct := 10.0 + pct*0.8
			job.setProgress(adjustedPct, info)
		}
	}

	if err := cmd.Wait(); err != nil {
		job.setError(fmt.Errorf("ffmpeg encode: %w", err), "ENCODE_ERROR")
		sendWebhook(job, "job.error")
		return
	}

	// Phase 4: Upload
	job.setPhase("uploading")
	err = transcode.UploadFile(ctx, outputPath, job.Request.OutputURL, func(transferred, total int64) {
		if total > 0 {
			pct := float64(transferred) / float64(total) * 100.0
			job.mu.Lock()
			job.Progress = 90.0 + pct*0.1 // upload is 90-100% of total progress
			job.mu.Unlock()
		}
	})
	if err != nil {
		job.setError(fmt.Errorf("upload output: %w", err), "UPLOAD_ERROR")
		sendWebhook(job, "job.error")
		return
	}

	// Probe output for metadata
	outProbeCmd := transcode.ProbeCmd(outputPath)
	if outProbeOutput, err := outProbeCmd.Output(); err == nil {
		if outProbe, err := transcode.ParseProbeOutput(outProbeOutput); err == nil {
			stat, _ := os.Stat(outputPath)
			var fileSize int64
			if stat != nil {
				fileSize = stat.Size()
			}
			job.setOutputInfo(outProbe, fileSize)
		}
	}

	// Phase 5: Thumbnail extraction (if requested)
	if job.Request.ThumbnailURL != "" {
		job.setPhase("thumbnail")
		thumbPath := filepath.Join(jobTempDir, "thumbnail.jpg")
		seekTime := transcode.ResolveSeekTime(job.Request.ThumbnailSeek, probe.Duration)
		thumbCmd := transcode.ThumbnailCmd(inputPath, thumbPath, seekTime,
			job.Request.ThumbnailWidth, job.Request.ThumbnailHeight, hw)
		if thumbOutput, err := thumbCmd.CombinedOutput(); err != nil {
			log.Printf("[job %s] thumbnail extraction failed: %v (output: %s)", job.ID, err, string(thumbOutput))
			// Non-fatal: continue to completion
		} else if err := transcode.UploadFile(ctx, thumbPath, job.Request.ThumbnailURL, nil); err != nil {
			log.Printf("[job %s] thumbnail upload failed: %v", job.ID, err)
			// Non-fatal: continue to completion
		}
	}

	// Phase 6: Complete
	job.setComplete()
	sendWebhook(job, "job.complete")

	// Suppress unused variable warning for totalDuration
	_ = totalDuration
}

// scanFFmpegLines is a bufio.SplitFunc that splits on \r or \n.
// ffmpeg outputs progress lines with \r (carriage return).
func scanFFmpegLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// ── Webhooks ──

func sendWebhook(job *Job, event string) {
	url := job.Request.WebhookURL
	if url == "" {
		return
	}

	payload := map[string]interface{}{
		"event":     event,
		"job_id":    job.ID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"data":      job.toResponse(),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[job %s] webhook marshal error: %v", job.ID, err)
		return
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	// Retry with backoff: 1s, 5s, 25s
	delays := []time.Duration{0, 1 * time.Second, 5 * time.Second, 25 * time.Second}
	for attempt, delay := range delays {
		if attempt > 0 {
			time.Sleep(delay)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
		if err != nil {
			cancel()
			log.Printf("[job %s] webhook request error (attempt %d): %v", job.ID, attempt+1, err)
			continue
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Webhook-Event", event)
		req.Header.Set("X-Webhook-Job-Id", job.ID)
		req.Header.Set("X-Webhook-Timestamp", timestamp)

		// HMAC-SHA256 signature
		if job.Request.WebhookSecret != "" {
			mac := hmac.New(sha256.New, []byte(job.Request.WebhookSecret))
			mac.Write([]byte(timestamp + "." + string(body)))
			sig := hex.EncodeToString(mac.Sum(nil))
			req.Header.Set("X-Webhook-Signature", sig)
		}

		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err != nil {
			log.Printf("[job %s] webhook send error (attempt %d): %v", job.ID, attempt+1, err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			log.Printf("[job %s] webhook %s delivered (attempt %d)", job.ID, event, attempt+1)
			return
		}
		log.Printf("[job %s] webhook %s returned HTTP %d (attempt %d)", job.ID, event, resp.StatusCode, attempt+1)
	}

	log.Printf("[job %s] webhook %s failed after all retries", job.ID, event)
}

// ── Cleanup ──

func cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		jobsMu.Lock()
		for id, job := range jobs {
			job.mu.Lock()
			if (job.Status == "complete" || job.Status == "error") && !job.CompletedAt.IsZero() && now.Sub(job.CompletedAt) > jobTTL {
				delete(jobs, id)
				log.Printf("[cleanup] removed job %s (completed %s ago)", id, now.Sub(job.CompletedAt).Round(time.Second))
			}
			job.mu.Unlock()
		}
		jobsMu.Unlock()
	}
}

// ── Helpers ──

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func generateJobID() string {
	return fmt.Sprintf("tc-%d-%s", time.Now().UnixMilli(), randomHex(4))
}

func randomHex(n int) string {
	b := make([]byte, n)
	// Use crypto/rand for proper randomness would be better,
	// but for job IDs, timestamp + simple counter is fine
	for i := range b {
		b[i] = byte(time.Now().UnixNano() >> (i * 8))
	}
	return hex.EncodeToString(b)
}

func presetNames() []string {
	names := make([]string, len(presets))
	for i, p := range presets {
		names[i] = p.Name
	}
	return names
}

func guessExtension(url string) string {
	// Strip query params
	if idx := strings.IndexByte(url, '?'); idx >= 0 {
		url = url[:idx]
	}
	ext := filepath.Ext(url)
	if ext == "" {
		return ".mp4"
	}
	return ext
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// ── Main ──

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("transcode-runner starting...")

	// Create temp directory
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		log.Fatalf("create temp dir %s: %v", tempDir, err)
	}

	// Detect GPU
	hw = transcode.DetectGPU()
	if hw.IsGPUAvailable() {
		log.Printf("GPU detected: %s (%d MB VRAM)", hw.GPUName, hw.VRAM_MB)
		log.Printf("  Encoders: %s", strings.Join(hw.Encoders, ", "))
		log.Printf("  Decoders: %s", strings.Join(hw.Decoders, ", "))
		log.Printf("  HW Accels: %s", strings.Join(hw.HWAccels, ", "))
		if hw.MaxSessions > 0 {
			log.Printf("  Max concurrent sessions: %d", hw.MaxSessions)
		} else {
			log.Printf("  Max concurrent sessions: unlimited")
		}
	} else {
		log.Println("WARNING: No GPU detected — only software encoding will be available")
	}

	// Load presets
	presetsData := defaultPresetsYAML
	if presetsFile := os.Getenv("PRESETS_FILE"); presetsFile != "" {
		data, err := os.ReadFile(presetsFile)
		if err != nil {
			log.Fatalf("read presets file %s: %v", presetsFile, err)
		}
		presetsData = data
		log.Printf("Loaded presets from %s", presetsFile)
	}

	allPresets, err := transcode.LoadPresetsFromBytes(presetsData)
	if err != nil {
		log.Fatalf("load presets: %v", err)
	}

	var skipped []string
	presets, skipped = transcode.ValidatePresets(allPresets, hw)
	log.Printf("Presets: %d loaded, %d active, %d skipped", len(allPresets), len(presets), len(skipped))
	if len(skipped) > 0 {
		log.Printf("  Skipped (GPU not capable): %s", strings.Join(skipped, ", "))
	}
	for _, p := range presets {
		encoder := transcode.EncoderForCodec(p.VideoCodec, hw)
		log.Printf("  [%s] %s → %s (%dx%d @ %s)", p.Name, p.VideoCodec, encoder, p.Width, p.Height, p.Bitrate)
	}

	// Start cleanup goroutine
	go cleanupLoop()

	// HTTP routes
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/video/transcode", handleSubmit)
	mux.HandleFunc("/v1/video/transcode/status", handleStatus)
	mux.HandleFunc("/v1/video/transcode/presets", handlePresets)
	mux.HandleFunc("/healthz", handleHealthz)

	server := &http.Server{
		Addr:         runnerAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("Received %v, shutting down...", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	log.Printf("Listening on %s", runnerAddr)
	log.Printf("Config: max_queue=%d, temp_dir=%s, job_ttl=%s", maxQueueSize, tempDir, jobTTL)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
	log.Println("Server stopped")
}
