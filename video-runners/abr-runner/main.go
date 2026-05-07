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
	maxQueueSize = envInt("MAX_QUEUE_SIZE", 2)
	tempDir      = env("TEMP_DIR", "/tmp/abr")
	jobTTL       = time.Duration(envInt("JOB_TTL_SECONDS", 3600)) * time.Second
)

// ── Global state ──

var (
	jobs       = make(map[string]*ABRJob)
	jobsMu     sync.RWMutex
	activeJobs atomic.Int32
	hw         transcode.HWProfile
	abrPresets []transcode.ABRPreset
)

// ── Request / Response types ──

type ABRRequest struct {
	InputURL      string        `json:"input_url"`
	OutputURLs    ABROutputURLs `json:"output_urls"`
	Preset        string        `json:"preset"`
	WebhookURL    string        `json:"webhook_url,omitempty"`
	WebhookSecret string        `json:"webhook_secret,omitempty"`
}

type ABROutputURLs struct {
	Manifest   string                         `json:"manifest"`
	Renditions map[string]RenditionOutputURLs `json:"renditions"`
}

type RenditionOutputURLs struct {
	Playlist string `json:"playlist"`
	Stream   string `json:"stream"`
}

type StatusRequest struct {
	JobID string `json:"job_id"`
}

type ABRJobStatusResponse struct {
	JobID           string            `json:"job_id"`
	Status          string            `json:"status"`
	Phase           string            `json:"phase"`
	OverallProgress float64           `json:"overall_progress"`
	ManifestURL     string            `json:"manifest_url,omitempty"`
	Renditions      []RenditionStatus `json:"renditions"`
	InputInfo       *ProbeInfo        `json:"input,omitempty"`
	Error           string            `json:"error,omitempty"`
	ErrorCode       string            `json:"error_code,omitempty"`
	ProcessingTime  float64           `json:"processing_time_seconds,omitempty"`
	GPU             string            `json:"gpu,omitempty"`
	CreatedAt       string            `json:"created_at"`
	CompletedAt     string            `json:"completed_at,omitempty"`
}

type RenditionStatus struct {
	Name        string  `json:"name"`
	Status      string  `json:"status"` // pending, encoding, uploading, complete, error
	Progress    float64 `json:"progress"`
	EncodingFPS float64 `json:"encoding_fps,omitempty"`
	Speed       string  `json:"speed,omitempty"`
	Bitrate     int     `json:"bitrate,omitempty"`
	FileSize    int64   `json:"file_size,omitempty"`
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

// ── Job ──

type ABRJob struct {
	mu              sync.Mutex
	ID              string
	Status          string // queued, downloading, probing, encoding, packaging, uploading, complete, error
	Phase           string
	OverallProgress float64
	InputInfo       *ProbeInfo
	Error           string
	ErrorCode       string
	Request         ABRRequest
	Preset          transcode.ABRPreset
	Renditions      []renditionState
	CreatedAt       time.Time
	CompletedAt     time.Time
	StartedAt       time.Time
}

type renditionState struct {
	Name        string
	Status      string // pending, encoding, uploading, complete, error
	Progress    float64
	EncodingFPS float64
	Speed       float64
	Bitrate     int
	FileSize    int64
}

func (j *ABRJob) setPhase(phase string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = phase
	j.Phase = phase
	log.Printf("[job %s] phase: %s", j.ID, phase)
}

func (j *ABRJob) setRenditionStatus(idx int, status string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Renditions[idx].Status = status
	log.Printf("[job %s] rendition %s: %s", j.ID, j.Renditions[idx].Name, status)
}

func (j *ABRJob) setRenditionProgress(idx int, pct float64, info transcode.ProgressInfo) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Renditions[idx].Progress = pct
	j.Renditions[idx].EncodingFPS = info.FPS
	j.Renditions[idx].Speed = info.Speed
	j.updateOverallProgressLocked()
}

func (j *ABRJob) setRenditionComplete(idx int, bitrate int, fileSize int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Renditions[idx].Status = "complete"
	j.Renditions[idx].Progress = 100.0
	j.Renditions[idx].Bitrate = bitrate
	j.Renditions[idx].FileSize = fileSize
	j.updateOverallProgressLocked()
	log.Printf("[job %s] rendition %s complete (bitrate=%d, size=%d)",
		j.ID, j.Renditions[idx].Name, bitrate, fileSize)
}

func (j *ABRJob) updateOverallProgressLocked() {
	total := len(j.Renditions)
	if total == 0 {
		return
	}
	var sum float64
	for _, r := range j.Renditions {
		sum += r.Progress
	}
	j.OverallProgress = sum / float64(total)
}

func (j *ABRJob) setInputInfo(probe transcode.ProbeResult) {
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

func (j *ABRJob) setComplete() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = "complete"
	j.Phase = "complete"
	j.OverallProgress = 100.0
	j.CompletedAt = time.Now()
	log.Printf("[job %s] complete (%.1fs)", j.ID, j.CompletedAt.Sub(j.StartedAt).Seconds())
}

func (j *ABRJob) setError(err error, code string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = "error"
	j.Phase = "error"
	j.Error = err.Error()
	j.ErrorCode = code
	j.CompletedAt = time.Now()
	log.Printf("[job %s] error (%s): %v", j.ID, code, err)
}

func (j *ABRJob) toResponse() ABRJobStatusResponse {
	j.mu.Lock()
	defer j.mu.Unlock()

	resp := ABRJobStatusResponse{
		JobID:           j.ID,
		Status:          j.Status,
		Phase:           j.Phase,
		OverallProgress: j.OverallProgress,
		GPU:             hw.GPUName,
		CreatedAt:       j.CreatedAt.UTC().Format(time.RFC3339),
	}

	if j.InputInfo != nil {
		resp.InputInfo = j.InputInfo
	}

	resp.Renditions = make([]RenditionStatus, len(j.Renditions))
	for i, r := range j.Renditions {
		rs := RenditionStatus{
			Name:        r.Name,
			Status:      r.Status,
			Progress:    r.Progress,
			EncodingFPS: r.EncodingFPS,
			Bitrate:     r.Bitrate,
			FileSize:    r.FileSize,
		}
		if r.Speed > 0 {
			rs.Speed = fmt.Sprintf("%.2fx", r.Speed)
		}
		resp.Renditions[i] = rs
	}

	if j.Status == "complete" {
		resp.ManifestURL = j.Request.OutputURLs.Manifest
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

	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
		return
	}

	var req ABRRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	// Validate required fields
	if req.InputURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "input_url is required"})
		return
	}
	if req.Preset == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "preset is required"})
		return
	}

	// Validate preset exists
	preset, ok := transcode.FindABRPreset(abrPresets, req.Preset)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":             fmt.Sprintf("unknown preset: %s", req.Preset),
			"available_presets": strings.Join(presetNames(), ", "),
		})
		return
	}

	// Validate output URLs
	if req.OutputURLs.Manifest == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "output_urls.manifest is required"})
		return
	}
	for _, rend := range preset.Renditions {
		urls, ok := req.OutputURLs.Renditions[rend.Name]
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("missing output_urls for rendition %q", rend.Name),
			})
			return
		}
		if urls.Playlist == "" || urls.Stream == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("rendition %q requires both playlist and stream URLs", rend.Name),
			})
			return
		}
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

	// Create job with rendition states
	jobID := generateJobID()
	renditions := make([]renditionState, len(preset.Renditions))
	for i, rend := range preset.Renditions {
		renditions[i] = renditionState{
			Name:   rend.Name,
			Status: "pending",
		}
	}

	job := &ABRJob{
		ID:         jobID,
		Status:     "queued",
		Phase:      "queued",
		Request:    req,
		Preset:     preset,
		Renditions: renditions,
		CreatedAt:  time.Now(),
		StartedAt:  time.Now(),
	}

	jobsMu.Lock()
	jobs[jobID] = job
	jobsMu.Unlock()

	activeJobs.Add(1)
	go runABRJob(job)

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"job_id":     jobID,
		"status":     "queued",
		"preset":     preset.Name,
		"renditions": preset.RenditionNames(),
	})
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
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
		Presets []transcode.ABRPreset `json:"presets"`
		GPU     string                `json:"gpu"`
		Count   int                   `json:"count"`
	}
	writeJSON(w, http.StatusOK, presetInfo{
		Presets: abrPresets,
		GPU:     hw.GPUName,
		Count:   len(abrPresets),
	})
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":      "ok",
		"gpu":         hw.GPUName,
		"vram_mb":     hw.VRAM_MB,
		"active_jobs": activeJobs.Load(),
		"max_jobs":    maxQueueSize,
		"presets":     len(abrPresets),
	})
}

// ── Job Worker ──

func runABRJob(job *ABRJob) {
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

	// Phase 1: Download
	job.setPhase("downloading")
	ctx := context.Background()
	_, err := transcode.DownloadFile(ctx, job.Request.InputURL, inputPath, func(transferred, total int64) {
		if total > 0 {
			pct := float64(transferred) / float64(total) * 100.0
			job.mu.Lock()
			job.OverallProgress = pct * 0.05 // download is 0-5% of total
			job.mu.Unlock()
		}
	})
	if err != nil {
		job.setError(fmt.Errorf("download input: %w", err), "DOWNLOAD_ERROR")
		sendWebhook(job, "job.error")
		return
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

	totalDur := time.Duration(probe.Duration * float64(time.Second))

	// Phase 3: Encode renditions sequentially
	job.setPhase("encoding")
	sendWebhook(job, "job.encoding")

	renditionPlaylistPaths := map[string]string{} // for master manifest generation

	for i, rend := range job.Preset.Renditions {
		rendDir := filepath.Join(jobTempDir, rend.Name)
		if err := os.MkdirAll(rendDir, 0755); err != nil {
			job.setError(fmt.Errorf("create rendition dir %s: %w", rend.Name, err), "TEMP_DIR_ERROR")
			sendWebhook(job, "job.error")
			return
		}

		job.setRenditionStatus(i, "encoding")

		cmd := transcode.HLSRenditionCmd(inputPath, rendDir, rend, job.Preset.SegmentDuration, hw, probe)
		stderr, err := cmd.StderrPipe()
		if err != nil {
			job.setError(fmt.Errorf("create stderr pipe for %s: %w", rend.Name, err), "ENCODE_ERROR")
			sendWebhook(job, "job.error")
			return
		}

		if err := cmd.Start(); err != nil {
			job.setError(fmt.Errorf("start ffmpeg for %s: %w", rend.Name, err), "ENCODE_ERROR")
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
				job.setRenditionProgress(i, pct, info)
			}
		}

		if err := cmd.Wait(); err != nil {
			job.setError(fmt.Errorf("ffmpeg encode %s: %w", rend.Name, err), "ENCODE_ERROR")
			sendWebhook(job, "job.error")
			return
		}

		// Upload rendition files immediately (progressive upload)
		job.setRenditionStatus(i, "uploading")
		outputURLs := job.Request.OutputURLs.Renditions[rend.Name]

		// Upload playlist.m3u8
		playlistPath := filepath.Join(rendDir, "playlist.m3u8")
		if err := transcode.UploadFile(ctx, playlistPath, outputURLs.Playlist, nil); err != nil {
			job.setError(fmt.Errorf("upload playlist for %s: %w", rend.Name, err), "UPLOAD_ERROR")
			sendWebhook(job, "job.error")
			return
		}

		// Upload stream.mp4
		streamPath := filepath.Join(rendDir, "stream.mp4")
		if err := transcode.UploadFile(ctx, streamPath, outputURLs.Stream, nil); err != nil {
			job.setError(fmt.Errorf("upload stream for %s: %w", rend.Name, err), "UPLOAD_ERROR")
			sendWebhook(job, "job.error")
			return
		}

		// Probe output for actual bitrate + file size
		var actualBitrate int
		var fileSize int64
		if stat, err := os.Stat(streamPath); err == nil {
			fileSize = stat.Size()
		}
		outProbeCmd := transcode.ProbeCmd(streamPath)
		if outProbeOutput, err := outProbeCmd.Output(); err == nil {
			if outProbe, err := transcode.ParseProbeOutput(outProbeOutput); err == nil {
				actualBitrate = outProbe.Bitrate
			}
		}

		job.setRenditionComplete(i, actualBitrate, fileSize)
		sendWebhook(job, "job.rendition.complete")

		// Store relative path for master manifest
		renditionPlaylistPaths[rend.Name] = rend.Name + "/playlist.m3u8"

		// Delete rendition temp files to free disk for next rendition
		os.RemoveAll(rendDir)
	}

	// Phase 4: Generate and upload master manifest
	job.setPhase("packaging")
	masterManifest := transcode.GenerateMasterPlaylist(job.Preset.Renditions, renditionPlaylistPaths)

	masterPath := filepath.Join(jobTempDir, "master.m3u8")
	if err := os.WriteFile(masterPath, []byte(masterManifest), 0644); err != nil {
		job.setError(fmt.Errorf("write master manifest: %w", err), "MANIFEST_ERROR")
		sendWebhook(job, "job.error")
		return
	}

	job.setPhase("uploading")
	if err := transcode.UploadFile(ctx, masterPath, job.Request.OutputURLs.Manifest, nil); err != nil {
		job.setError(fmt.Errorf("upload master manifest: %w", err), "UPLOAD_ERROR")
		sendWebhook(job, "job.error")
		return
	}

	// Phase 5: Complete
	job.setComplete()
	sendWebhook(job, "job.complete")
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

func sendWebhook(job *ABRJob, event string) {
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
	return fmt.Sprintf("abr-%d-%s", time.Now().UnixMilli(), randomHex(4))
}

func randomHex(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(time.Now().UnixNano() >> (i * 8))
	}
	return hex.EncodeToString(b)
}

func presetNames() []string {
	names := make([]string, len(abrPresets))
	for i, p := range abrPresets {
		names[i] = p.Name
	}
	return names
}

func guessExtension(url string) string {
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
	log.Println("abr-runner starting...")

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

	// Load ABR presets
	presetsData := defaultPresetsYAML
	if presetsFile := os.Getenv("PRESETS_FILE"); presetsFile != "" {
		data, err := os.ReadFile(presetsFile)
		if err != nil {
			log.Fatalf("read presets file %s: %v", presetsFile, err)
		}
		presetsData = data
		log.Printf("Loaded ABR presets from %s", presetsFile)
	}

	allPresets, err := transcode.LoadABRPresetsFromBytes(presetsData)
	if err != nil {
		log.Fatalf("load ABR presets: %v", err)
	}

	var skipped []string
	abrPresets, skipped = transcode.ValidateABRPresets(allPresets, hw)
	log.Printf("ABR Presets: %d loaded, %d active, %d skipped", len(allPresets), len(abrPresets), len(skipped))
	if len(skipped) > 0 {
		log.Printf("  Skipped (GPU not capable): %s", strings.Join(skipped, ", "))
	}
	for _, p := range abrPresets {
		names := p.RenditionNames()
		log.Printf("  [%s] %d renditions: %s", p.Name, len(names), strings.Join(names, ", "))
	}

	// Start cleanup goroutine
	go cleanupLoop()

	// HTTP routes
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/video/transcode/abr", handleSubmit)
	mux.HandleFunc("/v1/video/transcode/abr/status", handleStatus)
	mux.HandleFunc("/v1/video/transcode/abr/presets", handlePresets)
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
