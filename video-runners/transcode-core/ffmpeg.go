package transcode

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ProbeResult holds parsed ffprobe output for a media file.
type ProbeResult struct {
	Duration       float64 `json:"duration"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	VideoCodec     string  `json:"video_codec"`
	AudioCodec     string  `json:"audio_codec"`
	FPS            float64 `json:"fps"`
	Bitrate        int     `json:"bitrate"`
	PixFmt         string  `json:"pix_fmt"`
	ColorTransfer  string  `json:"color_transfer"`  // "smpte2084" (PQ/HDR10), "arib-std-b67" (HLG)
	ColorPrimaries string  `json:"color_primaries"` // "bt2020"
	ColorSpace     string  `json:"color_space"`     // "bt2020nc"
}

// IsHDR returns true if the video has HDR color transfer characteristics.
func (p ProbeResult) IsHDR() bool {
	return p.ColorTransfer == "smpte2084" || p.ColorTransfer == "arib-std-b67"
}

// ffprobeOutput mirrors the JSON structure from ffprobe.
type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

type ffprobeStream struct {
	CodecType      string `json:"codec_type"`
	CodecName      string `json:"codec_name"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	PixFmt         string `json:"pix_fmt"`
	RFrameRate     string `json:"r_frame_rate"`
	AvgFrameRate   string `json:"avg_frame_rate"`
	ColorTransfer  string `json:"color_transfer"`
	ColorSpace     string `json:"color_space"`
	ColorPrimaries string `json:"color_primaries"`
}

type ffprobeFormat struct {
	Duration string `json:"duration"`
	BitRate  string `json:"bit_rate"`
}

// ProbeCmd returns an exec.Cmd for probing a media file.
func ProbeCmd(inputPath string) *exec.Cmd {
	return exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		inputPath,
	)
}

// ParseProbeOutput parses ffprobe JSON output into a ProbeResult.
func ParseProbeOutput(jsonData []byte) (ProbeResult, error) {
	var out ffprobeOutput
	if err := json.Unmarshal(jsonData, &out); err != nil {
		return ProbeResult{}, fmt.Errorf("parse ffprobe output: %w", err)
	}

	result := ProbeResult{}

	// Parse format-level fields
	if out.Format.Duration != "" {
		if d, err := strconv.ParseFloat(out.Format.Duration, 64); err == nil {
			result.Duration = d
		}
	}
	if out.Format.BitRate != "" {
		if b, err := strconv.Atoi(out.Format.BitRate); err == nil {
			result.Bitrate = b
		}
	}

	// Parse stream-level fields
	for _, s := range out.Streams {
		switch s.CodecType {
		case "video":
			if result.VideoCodec == "" {
				result.VideoCodec = s.CodecName
				result.Width = s.Width
				result.Height = s.Height
				result.PixFmt = s.PixFmt
				result.FPS = parseFrameRate(s.RFrameRate, s.AvgFrameRate)
				result.ColorTransfer = s.ColorTransfer
				result.ColorPrimaries = s.ColorPrimaries
				result.ColorSpace = s.ColorSpace
			}
		case "audio":
			if result.AudioCodec == "" {
				result.AudioCodec = s.CodecName
			}
		}
	}

	return result, nil
}

// TranscodeCmd builds the ffmpeg command for transcoding with the given preset and hardware profile.
func TranscodeCmd(inputPath, outputPath string, preset Preset, hw HWProfile, probe ProbeResult, opts TranscodeOptions) *exec.Cmd {
	args := []string{"-y"}

	// Hardware acceleration input — skip when software decode is needed
	// (subtitle burn-in and watermark overlay require CPU-side frames)
	if !opts.NeedsSoftwareDecode() {
		args = append(args, buildHWAccelInputArgs(hw)...)
	}

	args = append(args, "-i", inputPath)

	// Second input for watermark
	if opts.WatermarkPath != "" {
		args = append(args, "-i", opts.WatermarkPath)
	}

	// Video encoding args
	if opts.NeedsSoftwareDecode() {
		// Use GPU encoder but with software-decoded input
		args = append(args, buildVideoArgsSWDecode(preset, hw, probe)...)
	} else {
		args = append(args, buildVideoArgs(preset, hw, probe)...)
	}

	// Filter graph
	if opts.NeedsSoftwareDecode() {
		// Subtitles/watermarks present — software decode, use advanced filter graph
		// (includes CPU tonemap if HDR + ToneMap is set)
		filterType, filterStr := BuildAdvancedFilterGraph(opts, hw, probe, preset.Width, preset.Height)
		if filterStr != "" {
			if filterType == "filter_complex" {
				args = append(args, "-filter_complex", filterStr)
				args = append(args, "-map", "[out]", "-map", "0:a?")
			} else {
				args = append(args, "-vf", filterStr)
			}
		}
	} else if opts.ToneMap && probe.IsHDR() {
		// GPU tonemap (no software decode needed) — use GPU-specific tonemap filter
		if gpuTM := buildGPUTonemapFilter(hw); gpuTM != "" {
			existingFilter := buildFilterGraph(preset, hw, probe)
			if existingFilter != "" {
				args = append(args, "-vf", gpuTM+","+existingFilter)
			} else {
				args = append(args, "-vf", gpuTM)
			}
		}
	} else {
		// Standard filter graph (scaling only)
		if filters := buildFilterGraph(preset, hw, probe); filters != "" {
			args = append(args, "-vf", filters)
		}
	}

	// Audio encoding args
	args = append(args, buildAudioArgs(preset)...)

	// Output options
	args = append(args, "-movflags", "+faststart")
	args = append(args, outputPath)

	return exec.Command("ffmpeg", args...)
}

// buildVideoArgsSWDecode constructs video encoding arguments for software-decoded input.
// Still uses GPU encoder when available, but without hardware-specific pixel format management.
func buildVideoArgsSWDecode(preset Preset, hw HWProfile, probe ProbeResult) []string {
	args := []string{}

	encoder := EncoderForCodec(preset.VideoCodec, hw)
	args = append(args, "-c:v", encoder)

	// Vendor-specific tuning
	args = append(args, buildEncoderTuningArgs(encoder)...)

	// Bitrate
	if preset.Bitrate != "" {
		args = append(args, "-b:v", preset.Bitrate)
	}
	if preset.MaxRate != "" {
		args = append(args, "-maxrate", preset.MaxRate)
	}
	if preset.BufSize != "" {
		args = append(args, "-bufsize", preset.BufSize)
	}

	// Quality (CRF or vendor equivalent)
	args = append(args, buildQualityArgs(encoder, preset.CRF)...)

	// Pixel format — always set for software decode path since GPU isn't managing it
	if preset.PixFmt != "" {
		args = append(args, "-pix_fmt", preset.PixFmt)
	}

	// Profile
	if preset.Profile != "" {
		args = append(args, "-profile:v", preset.Profile)
	}

	// Tier (for HEVC)
	if preset.Tier != "" {
		args = append(args, "-tier", preset.Tier)
	}

	// FPS override
	if preset.FPS > 0 {
		args = append(args, "-r", strconv.Itoa(preset.FPS))
	}

	return args
}

// buildHWAccelInputArgs returns hwaccel flags for any vendor.
func buildHWAccelInputArgs(hw HWProfile) []string {
	switch hw.Vendor {
	case VendorNVIDIA:
		if hw.HasHWAccel("cuda") {
			return []string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"}
		}
	case VendorIntel:
		if hw.HasHWAccel("qsv") {
			return []string{"-hwaccel", "qsv", "-hwaccel_output_format", "qsv"}
		}
	case VendorAMD:
		if hw.HasHWAccel("vaapi") {
			args := []string{"-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi"}
			if hw.DevicePath != "" {
				args = append(args, "-hwaccel_device", hw.DevicePath)
			}
			return args
		}
	}
	return nil
}

// buildEncoderTuningArgs returns preset/tune/rc flags per vendor.
func buildEncoderTuningArgs(encoder string) []string {
	switch {
	case strings.Contains(encoder, "nvenc"):
		args := []string{"-preset", "p4", "-tune", "hq"}
		if strings.Contains(encoder, "h264") || strings.Contains(encoder, "hevc") {
			args = append(args, "-rc", "vbr")
		}
		return args
	case strings.Contains(encoder, "_qsv"):
		return []string{"-preset", "medium", "-look_ahead", "1"}
	case strings.Contains(encoder, "_vaapi"):
		return []string{"-rc_mode", "VBR"}
	}
	return nil
}

// buildQualityArgs returns CRF/quality args appropriate for the encoder.
func buildQualityArgs(encoder string, crf int) []string {
	if crf <= 0 {
		return nil
	}
	switch {
	case strings.Contains(encoder, "nvenc"), strings.Contains(encoder, "_vaapi"):
		// NVENC and VAAPI don't support CRF; quality is bitrate-driven
		return nil
	case strings.Contains(encoder, "_qsv"):
		return []string{"-global_quality", strconv.Itoa(crf)}
	default:
		// Software encoders
		return []string{"-crf", strconv.Itoa(crf)}
	}
}

// isHWManagedPixFmt returns true when the GPU manages pixel format internally.
func isHWManagedPixFmt(hw HWProfile) bool {
	return hw.Vendor == VendorNVIDIA || hw.Vendor == VendorIntel || hw.Vendor == VendorAMD
}

// buildVideoArgs constructs the video encoding arguments.
func buildVideoArgs(preset Preset, hw HWProfile, probe ProbeResult) []string {
	args := []string{}

	encoder := EncoderForCodec(preset.VideoCodec, hw)
	args = append(args, "-c:v", encoder)

	// Vendor-specific tuning
	args = append(args, buildEncoderTuningArgs(encoder)...)

	// Bitrate
	if preset.Bitrate != "" {
		args = append(args, "-b:v", preset.Bitrate)
	}
	if preset.MaxRate != "" {
		args = append(args, "-maxrate", preset.MaxRate)
	}
	if preset.BufSize != "" {
		args = append(args, "-bufsize", preset.BufSize)
	}

	// Quality (CRF or vendor equivalent)
	args = append(args, buildQualityArgs(encoder, preset.CRF)...)

	// Pixel format — GPU vendors manage this internally
	if preset.PixFmt != "" && !isHWManagedPixFmt(hw) {
		args = append(args, "-pix_fmt", preset.PixFmt)
	}

	// Profile
	if preset.Profile != "" {
		args = append(args, "-profile:v", preset.Profile)
	}

	// Tier (for HEVC)
	if preset.Tier != "" {
		args = append(args, "-tier", preset.Tier)
	}

	// FPS override
	if preset.FPS > 0 {
		args = append(args, "-r", strconv.Itoa(preset.FPS))
	}

	return args
}

// buildAudioArgs constructs the audio encoding arguments.
func buildAudioArgs(preset Preset) []string {
	args := []string{}

	if preset.AudioCodec != "" {
		args = append(args, "-c:a", preset.AudioCodec)
	} else {
		args = append(args, "-c:a", "aac")
	}

	if preset.AudioBitrate != "" {
		args = append(args, "-b:a", preset.AudioBitrate)
	}

	if preset.AudioChannels > 0 {
		args = append(args, "-ac", strconv.Itoa(preset.AudioChannels))
	}

	if preset.AudioSampleRate > 0 {
		args = append(args, "-ar", strconv.Itoa(preset.AudioSampleRate))
	}

	return args
}

// buildScaleFilter returns the appropriate scale filter for the hardware profile.
func buildScaleFilter(w, h int, hw HWProfile) string {
	if w <= 0 {
		w = -2
	}
	if h <= 0 {
		h = -2
	}

	switch hw.Vendor {
	case VendorNVIDIA:
		if hw.HasHWAccel("cuda") {
			return fmt.Sprintf("scale_cuda=%d:%d", w, h)
		}
	case VendorIntel:
		if hw.HasHWAccel("qsv") {
			return fmt.Sprintf("scale_qsv=w=%d:h=%d", w, h)
		}
	case VendorAMD:
		if hw.HasHWAccel("vaapi") {
			return fmt.Sprintf("scale_vaapi=w=%d:h=%d", w, h)
		}
	}
	return fmt.Sprintf("scale=%d:%d", w, h)
}

// buildFilterGraph constructs the full filter graph string.
func buildFilterGraph(preset Preset, hw HWProfile, probe ProbeResult) string {
	if preset.Width <= 0 && preset.Height <= 0 {
		return ""
	}
	// Only add scale if dimensions actually differ
	if preset.Width == probe.Width && preset.Height == probe.Height {
		return ""
	}

	filters := []string{}

	// AMD VAAPI requires hwupload before scale
	if hw.Vendor == VendorAMD && hw.HasHWAccel("vaapi") {
		filters = append(filters, "format=nv12", "hwupload")
	}

	filters = append(filters, buildScaleFilter(preset.Width, preset.Height, hw))

	return strings.Join(filters, ",")
}

// parseFrameRate parses ffprobe frame rate strings like "30/1" or "30000/1001".
func parseFrameRate(rFrameRate, avgFrameRate string) float64 {
	for _, fr := range []string{rFrameRate, avgFrameRate} {
		if fr == "" || fr == "0/0" {
			continue
		}
		parts := strings.Split(fr, "/")
		if len(parts) == 2 {
			num, err1 := strconv.ParseFloat(parts[0], 64)
			den, err2 := strconv.ParseFloat(parts[1], 64)
			if err1 == nil && err2 == nil && den > 0 {
				return num / den
			}
		}
		if f, err := strconv.ParseFloat(fr, 64); err == nil {
			return f
		}
	}
	return 0
}
