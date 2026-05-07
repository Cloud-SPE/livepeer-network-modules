package transcode

import (
	"strings"
	"testing"
)

func TestProbeCmd(t *testing.T) {
	cmd := ProbeCmd("/tmp/test.mp4")
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "ffprobe") {
		t.Error("expected ffprobe command")
	}
	if !strings.Contains(args, "-print_format json") {
		t.Error("expected JSON output format")
	}
	if !strings.Contains(args, "-show_format") {
		t.Error("expected -show_format")
	}
	if !strings.Contains(args, "-show_streams") {
		t.Error("expected -show_streams")
	}
	if !strings.Contains(args, "/tmp/test.mp4") {
		t.Error("expected input path in args")
	}
}

func TestParseProbeOutput(t *testing.T) {
	jsonData := []byte(`{
		"streams": [
			{
				"codec_type": "video",
				"codec_name": "h264",
				"width": 1920,
				"height": 1080,
				"pix_fmt": "yuv420p",
				"r_frame_rate": "30/1",
				"avg_frame_rate": "30/1"
			},
			{
				"codec_type": "audio",
				"codec_name": "aac",
				"r_frame_rate": "0/0",
				"avg_frame_rate": "0/0"
			}
		],
		"format": {
			"duration": "120.5",
			"bit_rate": "5000000"
		}
	}`)

	result, err := ParseProbeOutput(jsonData)
	if err != nil {
		t.Fatalf("ParseProbeOutput() error: %v", err)
	}

	if result.VideoCodec != "h264" {
		t.Errorf("VideoCodec = %q, want h264", result.VideoCodec)
	}
	if result.AudioCodec != "aac" {
		t.Errorf("AudioCodec = %q, want aac", result.AudioCodec)
	}
	if result.Width != 1920 || result.Height != 1080 {
		t.Errorf("Dimensions = %dx%d, want 1920x1080", result.Width, result.Height)
	}
	if result.Duration != 120.5 {
		t.Errorf("Duration = %f, want 120.5", result.Duration)
	}
	if result.Bitrate != 5000000 {
		t.Errorf("Bitrate = %d, want 5000000", result.Bitrate)
	}
	if result.FPS != 30.0 {
		t.Errorf("FPS = %f, want 30.0", result.FPS)
	}
	if result.PixFmt != "yuv420p" {
		t.Errorf("PixFmt = %q, want yuv420p", result.PixFmt)
	}
}

func TestParseProbeOutput_Invalid(t *testing.T) {
	_, err := ParseProbeOutput([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestTranscodeCmd_WithGPU(t *testing.T) {
	hw := HWProfile{
		GPUName:  "RTX 4090",
		Vendor:   VendorNVIDIA,
		Encoders: []string{"h264_nvenc", "hevc_nvenc"},
		HWAccels: []string{"cuda"},
	}
	preset := Preset{
		VideoCodec:    "h264",
		AudioCodec:    "aac",
		Width:         1920,
		Height:        1080,
		Bitrate:       "5M",
		MaxRate:       "7M",
		BufSize:       "14M",
		PixFmt:        "yuv420p",
		Profile:       "high",
		AudioBitrate:  "128k",
		AudioChannels: 2,
	}
	probe := ProbeResult{
		Width:      3840,
		Height:     2160,
		VideoCodec: "h264",
		Duration:   120.0,
	}

	cmd := TranscodeCmd("/tmp/input.mp4", "/tmp/output.mp4", preset, hw, probe, TranscodeOptions{})
	args := strings.Join(cmd.Args, " ")

	// GPU-accelerated encoding
	if !strings.Contains(args, "-hwaccel cuda") {
		t.Error("expected -hwaccel cuda")
	}
	if !strings.Contains(args, "-hwaccel_output_format cuda") {
		t.Error("expected -hwaccel_output_format cuda")
	}
	if !strings.Contains(args, "-c:v h264_nvenc") {
		t.Error("expected -c:v h264_nvenc")
	}
	if !strings.Contains(args, "-preset p4") {
		t.Error("expected NVENC preset p4")
	}
	if !strings.Contains(args, "-tune hq") {
		t.Error("expected NVENC tune hq")
	}

	// Bitrate settings
	if !strings.Contains(args, "-b:v 5M") {
		t.Error("expected -b:v 5M")
	}
	if !strings.Contains(args, "-maxrate 7M") {
		t.Error("expected -maxrate 7M")
	}
	if !strings.Contains(args, "-bufsize 14M") {
		t.Error("expected -bufsize 14M")
	}

	// Scale filter (input is 4K, output is 1080p)
	if !strings.Contains(args, "scale_cuda=1920:1080") {
		t.Error("expected scale_cuda=1920:1080 for GPU scaling")
	}

	// Audio
	if !strings.Contains(args, "-c:a aac") {
		t.Error("expected -c:a aac")
	}
	if !strings.Contains(args, "-b:a 128k") {
		t.Error("expected -b:a 128k")
	}

	// Output options
	if !strings.Contains(args, "-movflags +faststart") {
		t.Error("expected -movflags +faststart")
	}
}

func TestTranscodeCmd_WithoutGPU(t *testing.T) {
	hw := HWProfile{}
	preset := Preset{
		VideoCodec:    "h264",
		AudioCodec:    "aac",
		Width:         1280,
		Height:        720,
		Bitrate:       "3M",
		PixFmt:        "yuv420p",
		AudioBitrate:  "128k",
		AudioChannels: 2,
	}
	probe := ProbeResult{
		Width:  1920,
		Height: 1080,
	}

	cmd := TranscodeCmd("/tmp/input.mp4", "/tmp/output.mp4", preset, hw, probe, TranscodeOptions{})
	args := strings.Join(cmd.Args, " ")

	// Should use software encoder
	if !strings.Contains(args, "-c:v libx264") {
		t.Error("expected -c:v libx264 without GPU")
	}
	// Should NOT have hwaccel flags
	if strings.Contains(args, "-hwaccel") {
		t.Error("should not have -hwaccel without GPU")
	}
	// Software scale filter
	if !strings.Contains(args, "scale=1280:720") {
		t.Error("expected scale=1280:720 for software scaling")
	}
	// Pixel format set for software
	if !strings.Contains(args, "-pix_fmt yuv420p") {
		t.Error("expected -pix_fmt yuv420p for software encoder")
	}
}

func TestTranscodeCmd_SameDimensions(t *testing.T) {
	hw := HWProfile{
		GPUName:  "RTX 4090",
		Vendor:   VendorNVIDIA,
		Encoders: []string{"h264_nvenc"},
		HWAccels: []string{"cuda"},
	}
	preset := Preset{
		VideoCodec: "h264",
		Width:      1920,
		Height:     1080,
		Bitrate:    "5M",
	}
	probe := ProbeResult{
		Width:  1920,
		Height: 1080,
	}

	cmd := TranscodeCmd("/tmp/input.mp4", "/tmp/output.mp4", preset, hw, probe, TranscodeOptions{})
	args := strings.Join(cmd.Args, " ")

	// No scale filter needed when dimensions match
	if strings.Contains(args, "scale") {
		t.Error("should not have scale filter when dimensions match")
	}
}

func TestTranscodeCmd_WithIntelQSV(t *testing.T) {
	hw := HWProfile{
		GPUName:    "Intel(R) Arc A770",
		Vendor:     VendorIntel,
		DevicePath: "/dev/dri/renderD128",
		Encoders:   []string{"h264_qsv", "hevc_qsv", "av1_qsv"},
		HWAccels:   []string{"qsv", "vaapi"},
	}
	preset := Preset{
		VideoCodec:    "h264",
		AudioCodec:    "aac",
		Width:         1920,
		Height:        1080,
		Bitrate:       "5M",
		MaxRate:       "7M",
		BufSize:       "14M",
		PixFmt:        "yuv420p",
		Profile:       "high",
		AudioBitrate:  "128k",
		AudioChannels: 2,
	}
	probe := ProbeResult{Width: 3840, Height: 2160}

	cmd := TranscodeCmd("/tmp/input.mp4", "/tmp/output.mp4", preset, hw, probe, TranscodeOptions{})
	args := strings.Join(cmd.Args, " ")

	// QSV hwaccel
	if !strings.Contains(args, "-hwaccel qsv") {
		t.Errorf("expected -hwaccel qsv, got: %s", args)
	}
	if !strings.Contains(args, "-hwaccel_output_format qsv") {
		t.Errorf("expected -hwaccel_output_format qsv, got: %s", args)
	}
	// Should NOT have -hwaccel_device for Intel QSV
	if strings.Contains(args, "-hwaccel_device") {
		t.Error("Intel QSV should not use -hwaccel_device")
	}
	// QSV encoder
	if !strings.Contains(args, "-c:v h264_qsv") {
		t.Errorf("expected -c:v h264_qsv, got: %s", args)
	}
	// QSV tuning args
	if !strings.Contains(args, "-preset medium") {
		t.Errorf("expected -preset medium for QSV, got: %s", args)
	}
	if !strings.Contains(args, "-look_ahead 1") {
		t.Errorf("expected -look_ahead 1 for QSV, got: %s", args)
	}
	// Should NOT have NVENC tuning
	if strings.Contains(args, "-tune hq") {
		t.Error("should not have NVENC -tune hq for QSV")
	}
	// QSV scale filter
	if !strings.Contains(args, "scale_qsv=w=1920:h=1080") {
		t.Errorf("expected scale_qsv=w=1920:h=1080, got: %s", args)
	}
	// GPU-managed pix_fmt — should NOT be in args
	if strings.Contains(args, "-pix_fmt") {
		t.Error("should not have -pix_fmt for Intel QSV (GPU managed)")
	}
	// Bitrate
	if !strings.Contains(args, "-b:v 5M") {
		t.Error("expected -b:v 5M")
	}
}

func TestTranscodeCmd_WithAMDVAAPI(t *testing.T) {
	hw := HWProfile{
		GPUName:    "AMD Radeon RX 7900 XTX",
		Vendor:     VendorAMD,
		DevicePath: "/dev/dri/renderD128",
		Encoders:   []string{"h264_vaapi", "hevc_vaapi", "av1_vaapi"},
		HWAccels:   []string{"vaapi"},
	}
	preset := Preset{
		VideoCodec:    "h264",
		AudioCodec:    "aac",
		Width:         1920,
		Height:        1080,
		Bitrate:       "5M",
		MaxRate:       "7M",
		BufSize:       "14M",
		PixFmt:        "yuv420p",
		Profile:       "high",
		AudioBitrate:  "128k",
		AudioChannels: 2,
	}
	probe := ProbeResult{Width: 3840, Height: 2160}

	cmd := TranscodeCmd("/tmp/input.mp4", "/tmp/output.mp4", preset, hw, probe, TranscodeOptions{})
	args := strings.Join(cmd.Args, " ")

	// VAAPI hwaccel
	if !strings.Contains(args, "-hwaccel vaapi") {
		t.Errorf("expected -hwaccel vaapi, got: %s", args)
	}
	if !strings.Contains(args, "-hwaccel_output_format vaapi") {
		t.Errorf("expected -hwaccel_output_format vaapi, got: %s", args)
	}
	// AMD VAAPI needs -hwaccel_device
	if !strings.Contains(args, "-hwaccel_device /dev/dri/renderD128") {
		t.Errorf("expected -hwaccel_device /dev/dri/renderD128, got: %s", args)
	}
	// VAAPI encoder
	if !strings.Contains(args, "-c:v h264_vaapi") {
		t.Errorf("expected -c:v h264_vaapi, got: %s", args)
	}
	// VAAPI tuning args
	if !strings.Contains(args, "-rc_mode VBR") {
		t.Errorf("expected -rc_mode VBR for VAAPI, got: %s", args)
	}
	// Should NOT have NVENC or QSV tuning
	if strings.Contains(args, "-preset p4") || strings.Contains(args, "-preset medium") {
		t.Error("should not have NVENC/QSV preset for VAAPI")
	}
	// VAAPI scale filter with hwupload prefix
	if !strings.Contains(args, "format=nv12,hwupload,scale_vaapi=w=1920:h=1080") {
		t.Errorf("expected format=nv12,hwupload,scale_vaapi=w=1920:h=1080, got: %s", args)
	}
	// GPU-managed pix_fmt — should NOT be in args
	if strings.Contains(args, "-pix_fmt") {
		t.Error("should not have -pix_fmt for AMD VAAPI (GPU managed)")
	}
}

func TestTranscodeCmd_WithIntelQSV_CRF(t *testing.T) {
	hw := HWProfile{
		GPUName:  "Intel(R) Arc A770",
		Vendor:   VendorIntel,
		Encoders: []string{"h264_qsv"},
		HWAccels: []string{"qsv"},
	}
	preset := Preset{
		VideoCodec: "h264",
		Width:      1920,
		Height:     1080,
		CRF:        23,
	}
	probe := ProbeResult{Width: 1920, Height: 1080}

	cmd := TranscodeCmd("/tmp/input.mp4", "/tmp/output.mp4", preset, hw, probe, TranscodeOptions{})
	args := strings.Join(cmd.Args, " ")

	// QSV uses -global_quality instead of -crf
	if !strings.Contains(args, "-global_quality 23") {
		t.Errorf("expected -global_quality 23 for QSV, got: %s", args)
	}
	if strings.Contains(args, "-crf") {
		t.Error("should not have -crf for QSV")
	}
}

func TestParseProbeOutput_HDR(t *testing.T) {
	jsonData := []byte(`{
		"streams": [
			{
				"codec_type": "video",
				"codec_name": "hevc",
				"width": 3840,
				"height": 2160,
				"pix_fmt": "yuv420p10le",
				"r_frame_rate": "24/1",
				"avg_frame_rate": "24/1",
				"color_transfer": "smpte2084",
				"color_space": "bt2020nc",
				"color_primaries": "bt2020"
			},
			{
				"codec_type": "audio",
				"codec_name": "eac3",
				"r_frame_rate": "0/0",
				"avg_frame_rate": "0/0"
			}
		],
		"format": {
			"duration": "7200.0",
			"bit_rate": "25000000"
		}
	}`)

	result, err := ParseProbeOutput(jsonData)
	if err != nil {
		t.Fatalf("ParseProbeOutput() error: %v", err)
	}

	if result.ColorTransfer != "smpte2084" {
		t.Errorf("ColorTransfer = %q, want smpte2084", result.ColorTransfer)
	}
	if result.ColorPrimaries != "bt2020" {
		t.Errorf("ColorPrimaries = %q, want bt2020", result.ColorPrimaries)
	}
	if result.ColorSpace != "bt2020nc" {
		t.Errorf("ColorSpace = %q, want bt2020nc", result.ColorSpace)
	}
	if !result.IsHDR() {
		t.Error("expected IsHDR() = true for smpte2084")
	}
}

func TestIsHDR(t *testing.T) {
	tests := []struct {
		name     string
		transfer string
		want     bool
	}{
		{"HDR10 PQ", "smpte2084", true},
		{"HLG", "arib-std-b67", true},
		{"SDR bt709", "bt709", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := ProbeResult{ColorTransfer: tt.transfer}
			if got := p.IsHDR(); got != tt.want {
				t.Errorf("IsHDR() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTranscodeCmd_WithSubtitle(t *testing.T) {
	hw := HWProfile{
		GPUName:  "RTX 4090",
		Vendor:   VendorNVIDIA,
		Encoders: []string{"h264_nvenc"},
		HWAccels: []string{"cuda"},
	}
	preset := Preset{
		VideoCodec: "h264",
		Width:      1920,
		Height:     1080,
		Bitrate:    "5M",
	}
	probe := ProbeResult{Width: 3840, Height: 2160}
	opts := TranscodeOptions{SubtitlePath: "/tmp/subs.srt"}

	cmd := TranscodeCmd("/tmp/input.mp4", "/tmp/output.mp4", preset, hw, probe, opts)
	args := strings.Join(cmd.Args, " ")

	// Should NOT have hwaccel (software decode for subtitle burn-in)
	if strings.Contains(args, "-hwaccel") {
		t.Error("should not have -hwaccel when subtitle burn-in is active")
	}
	// Should still use GPU encoder
	if !strings.Contains(args, "-c:v h264_nvenc") {
		t.Error("expected GPU encoder h264_nvenc even with software decode")
	}
	// Should have subtitle filter
	if !strings.Contains(args, "subtitles=") {
		t.Errorf("expected subtitles filter, got: %s", args)
	}
	// Should use -vf (no watermark)
	if strings.Contains(args, "-filter_complex") {
		t.Error("should use -vf, not -filter_complex, without watermark")
	}
}

func TestTranscodeCmd_WithWatermark(t *testing.T) {
	hw := HWProfile{
		GPUName:  "RTX 4090",
		Vendor:   VendorNVIDIA,
		Encoders: []string{"h264_nvenc"},
		HWAccels: []string{"cuda"},
	}
	preset := Preset{
		VideoCodec: "h264",
		Width:      1920,
		Height:     1080,
		Bitrate:    "5M",
	}
	probe := ProbeResult{Width: 3840, Height: 2160}
	opts := TranscodeOptions{
		WatermarkPath:  "/tmp/logo.png",
		WatermarkPos:   "bottom-right",
		WatermarkScale: 0.1,
	}

	cmd := TranscodeCmd("/tmp/input.mp4", "/tmp/output.mp4", preset, hw, probe, opts)
	args := strings.Join(cmd.Args, " ")

	// Should NOT have hwaccel (software decode for watermark)
	if strings.Contains(args, "-hwaccel") {
		t.Error("should not have -hwaccel when watermark is active")
	}
	// Should have second input for watermark
	if !strings.Contains(args, "-i /tmp/logo.png") {
		t.Errorf("expected -i /tmp/logo.png, got: %s", args)
	}
	// Should use filter_complex
	if !strings.Contains(args, "-filter_complex") {
		t.Errorf("expected -filter_complex for watermark, got: %s", args)
	}
	// Should have overlay filter
	if !strings.Contains(args, "overlay=") {
		t.Errorf("expected overlay filter, got: %s", args)
	}
	// Should map output stream
	if !strings.Contains(args, "-map [out]") {
		t.Errorf("expected -map [out], got: %s", args)
	}
}

func TestTranscodeCmd_WithTonemap(t *testing.T) {
	hw := HWProfile{
		GPUName:  "RTX 4090",
		Vendor:   VendorNVIDIA,
		Encoders: []string{"h264_nvenc"},
		HWAccels: []string{"cuda"},
	}
	preset := Preset{
		VideoCodec: "h264",
		Width:      1920,
		Height:     1080,
		Bitrate:    "5M",
	}
	probe := ProbeResult{
		Width:         3840,
		Height:        2160,
		ColorTransfer: "smpte2084",
	}
	opts := TranscodeOptions{ToneMap: true}

	cmd := TranscodeCmd("/tmp/input.mp4", "/tmp/output.mp4", preset, hw, probe, opts)
	args := strings.Join(cmd.Args, " ")

	// Should have hwaccel (no subtitles/watermarks forcing software decode)
	if !strings.Contains(args, "-hwaccel cuda") {
		t.Error("expected -hwaccel cuda for tonemap-only")
	}
	// Should have GPU tonemap filter
	if !strings.Contains(args, "tonemap_cuda") {
		t.Errorf("expected tonemap_cuda filter, got: %s", args)
	}
}

func TestTranscodeCmd_TonemapWithSubtitle(t *testing.T) {
	hw := HWProfile{
		GPUName:  "RTX 4090",
		Vendor:   VendorNVIDIA,
		Encoders: []string{"h264_nvenc"},
		HWAccels: []string{"cuda"},
	}
	preset := Preset{
		VideoCodec: "h264",
		Bitrate:    "5M",
	}
	probe := ProbeResult{
		Width:         3840,
		Height:        2160,
		ColorTransfer: "smpte2084",
	}
	opts := TranscodeOptions{ToneMap: true, SubtitlePath: "/tmp/subs.srt"}

	cmd := TranscodeCmd("/tmp/input.mp4", "/tmp/output.mp4", preset, hw, probe, opts)
	args := strings.Join(cmd.Args, " ")

	// Should NOT have hwaccel (subtitle forces software decode)
	if strings.Contains(args, "-hwaccel") {
		t.Error("should not have -hwaccel when subtitle forces software decode")
	}
	// Should use CPU tonemap (zscale)
	if !strings.Contains(args, "zscale=") {
		t.Errorf("expected CPU tonemap (zscale) when software decode active, got: %s", args)
	}
	// Should have subtitle filter
	if !strings.Contains(args, "subtitles=") {
		t.Errorf("expected subtitles filter, got: %s", args)
	}
}

func TestParseFrameRate(t *testing.T) {
	tests := []struct {
		name       string
		rFrameRate string
		avgRate    string
		expected   float64
	}{
		{"30/1", "30/1", "30/1", 30.0},
		{"30000/1001 (NTSC)", "30000/1001", "30000/1001", 29.97002997002997},
		{"24/1", "24/1", "24/1", 24.0},
		{"fallback to avg", "0/0", "25/1", 25.0},
		{"empty", "", "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFrameRate(tt.rFrameRate, tt.avgRate)
			diff := got - tt.expected
			if diff < -0.01 || diff > 0.01 {
				t.Errorf("parseFrameRate(%q, %q) = %f, want %f", tt.rFrameRate, tt.avgRate, got, tt.expected)
			}
		})
	}
}
