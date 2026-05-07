package transcode

import (
	"testing"
)

const testPresetsYAML = `
presets:
  - name: h264-1080p
    description: "H.264 1080p at 5Mbps"
    video_codec: h264
    audio_codec: aac
    width: 1920
    height: 1080
    bitrate: "5M"
    max_rate: "7M"
    buf_size: "14M"
    pix_fmt: yuv420p
    profile: high
    gpu_required: true
    audio_bitrate: "128k"
    audio_channels: 2
  - name: h264-720p
    description: "H.264 720p at 3Mbps"
    video_codec: h264
    audio_codec: aac
    width: 1280
    height: 720
    bitrate: "3M"
    max_rate: "4.5M"
    buf_size: "9M"
    pix_fmt: yuv420p
    profile: high
    gpu_required: false
    audio_bitrate: "128k"
    audio_channels: 2
  - name: hevc-1080p
    description: "HEVC 1080p"
    video_codec: hevc
    audio_codec: aac
    width: 1920
    height: 1080
    bitrate: "4M"
    max_rate: "6M"
    buf_size: "12M"
    pix_fmt: yuv420p
    gpu_required: true
    audio_bitrate: "128k"
    audio_channels: 2
  - name: av1-1080p
    description: "AV1 1080p"
    video_codec: av1
    audio_codec: aac
    width: 1920
    height: 1080
    bitrate: "3M"
    max_rate: "4.5M"
    buf_size: "9M"
    pix_fmt: yuv420p
    gpu_required: true
    audio_bitrate: "128k"
    audio_channels: 2
`

func TestLoadPresetsFromBytes(t *testing.T) {
	presets, err := LoadPresetsFromBytes([]byte(testPresetsYAML))
	if err != nil {
		t.Fatalf("LoadPresetsFromBytes() error: %v", err)
	}
	if len(presets) != 4 {
		t.Fatalf("expected 4 presets, got %d", len(presets))
	}
	if presets[0].Name != "h264-1080p" {
		t.Errorf("first preset name = %q, want %q", presets[0].Name, "h264-1080p")
	}
	if presets[0].Width != 1920 || presets[0].Height != 1080 {
		t.Errorf("first preset dimensions = %dx%d, want 1920x1080", presets[0].Width, presets[0].Height)
	}
	if presets[0].Bitrate != "5M" {
		t.Errorf("first preset bitrate = %q, want %q", presets[0].Bitrate, "5M")
	}
}

func TestLoadPresetsFromBytes_Invalid(t *testing.T) {
	_, err := LoadPresetsFromBytes([]byte("not valid yaml: [[["))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}

	_, err = LoadPresetsFromBytes([]byte("presets: []"))
	if err == nil {
		t.Error("expected error for empty presets list")
	}
}

func TestValidatePresets(t *testing.T) {
	presets, _ := LoadPresetsFromBytes([]byte(testPresetsYAML))

	t.Run("with full GPU", func(t *testing.T) {
		hw := HWProfile{
			GPUName:  "RTX 4090",
			Vendor:   VendorNVIDIA,
			Encoders: []string{"h264_nvenc", "hevc_nvenc", "av1_nvenc"},
		}
		valid, skipped := ValidatePresets(presets, hw)
		if len(valid) != 4 {
			t.Errorf("expected 4 valid presets with full GPU, got %d (skipped: %v)", len(valid), skipped)
		}
		if len(skipped) != 0 {
			t.Errorf("expected 0 skipped, got %d: %v", len(skipped), skipped)
		}
	})

	t.Run("without GPU", func(t *testing.T) {
		hw := HWProfile{}
		valid, skipped := ValidatePresets(presets, hw)
		// h264-720p is gpu_required: false, so it should pass
		if len(valid) != 1 {
			t.Errorf("expected 1 valid preset without GPU, got %d", len(valid))
		}
		if len(skipped) != 3 {
			t.Errorf("expected 3 skipped, got %d: %v", len(skipped), skipped)
		}
	})

	t.Run("GPU without AV1 encoder", func(t *testing.T) {
		hw := HWProfile{
			GPUName:  "RTX 2080",
			Vendor:   VendorNVIDIA,
			Encoders: []string{"h264_nvenc", "hevc_nvenc"},
		}
		valid, skipped := ValidatePresets(presets, hw)
		// h264-1080p, h264-720p, hevc-1080p pass; av1-1080p skipped
		if len(valid) != 3 {
			t.Errorf("expected 3 valid, got %d (skipped: %v)", len(valid), skipped)
		}
		if len(skipped) != 1 || skipped[0] != "av1-1080p" {
			t.Errorf("expected skipped=[av1-1080p], got %v", skipped)
		}
	})
}

func TestValidatePresets_Intel(t *testing.T) {
	presets, _ := LoadPresetsFromBytes([]byte(testPresetsYAML))

	t.Run("Intel with full QSV support", func(t *testing.T) {
		hw := HWProfile{
			GPUName:  "Intel(R) Arc A770",
			Vendor:   VendorIntel,
			Encoders: []string{"h264_qsv", "hevc_qsv", "av1_qsv"},
		}
		valid, skipped := ValidatePresets(presets, hw)
		if len(valid) != 4 {
			t.Errorf("expected 4 valid presets with full Intel QSV, got %d (skipped: %v)", len(valid), skipped)
		}
	})

	t.Run("Intel without AV1", func(t *testing.T) {
		hw := HWProfile{
			GPUName:  "Intel(R) UHD 630",
			Vendor:   VendorIntel,
			Encoders: []string{"h264_qsv", "hevc_qsv"},
		}
		valid, skipped := ValidatePresets(presets, hw)
		if len(valid) != 3 {
			t.Errorf("expected 3 valid, got %d (skipped: %v)", len(valid), skipped)
		}
		if len(skipped) != 1 || skipped[0] != "av1-1080p" {
			t.Errorf("expected skipped=[av1-1080p], got %v", skipped)
		}
	})
}

func TestFindPreset(t *testing.T) {
	presets, _ := LoadPresetsFromBytes([]byte(testPresetsYAML))

	p, ok := FindPreset(presets, "h264-1080p")
	if !ok {
		t.Fatal("expected to find h264-1080p")
	}
	if p.Name != "h264-1080p" {
		t.Errorf("got name %q", p.Name)
	}

	// Case insensitive
	p, ok = FindPreset(presets, "H264-1080P")
	if !ok {
		t.Fatal("expected case-insensitive match for H264-1080P")
	}

	// Not found
	_, ok = FindPreset(presets, "nonexistent")
	if ok {
		t.Error("expected not found for nonexistent preset")
	}
}

func TestEncoderForCodec(t *testing.T) {
	hwGPU := HWProfile{
		GPUName:  "RTX 4090",
		Vendor:   VendorNVIDIA,
		Encoders: []string{"h264_nvenc", "hevc_nvenc", "av1_nvenc"},
	}
	hwNone := HWProfile{}

	tests := []struct {
		name     string
		codec    string
		hw       HWProfile
		expected string
	}{
		{"h264 with GPU", "h264", hwGPU, "h264_nvenc"},
		{"h264 without GPU", "h264", hwNone, "libx264"},
		{"hevc with GPU", "hevc", hwGPU, "hevc_nvenc"},
		{"hevc without GPU", "hevc", hwNone, "libx265"},
		{"av1 with GPU", "av1", hwGPU, "av1_nvenc"},
		{"av1 without GPU", "av1", hwNone, "libsvtav1"},
		{"vp9 any", "vp9", hwGPU, "libvpx-vp9"},
		{"avc alias", "avc", hwGPU, "h264_nvenc"},
		{"h265 alias", "h265", hwGPU, "hevc_nvenc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EncoderForCodec(tt.codec, tt.hw)
			if got != tt.expected {
				t.Errorf("EncoderForCodec(%q) = %q, want %q", tt.codec, got, tt.expected)
			}
		})
	}
}

func TestEncoderForCodec_Intel(t *testing.T) {
	hw := HWProfile{
		GPUName:  "Intel(R) Arc A770",
		Vendor:   VendorIntel,
		Encoders: []string{"h264_qsv", "hevc_qsv", "av1_qsv", "h264_vaapi", "hevc_vaapi"},
	}

	tests := []struct {
		codec    string
		expected string
	}{
		{"h264", "h264_qsv"},
		{"hevc", "hevc_qsv"},
		{"av1", "av1_qsv"},
		{"vp9", "libvpx-vp9"}, // no vp9_vaapi in this encoder list
	}

	for _, tt := range tests {
		t.Run(tt.codec, func(t *testing.T) {
			got := EncoderForCodec(tt.codec, hw)
			if got != tt.expected {
				t.Errorf("EncoderForCodec(%q) = %q, want %q", tt.codec, got, tt.expected)
			}
		})
	}
}

func TestEncoderForCodec_AMD(t *testing.T) {
	hw := HWProfile{
		GPUName:  "AMD Radeon RX 7900 XTX",
		Vendor:   VendorAMD,
		Encoders: []string{"h264_vaapi", "hevc_vaapi", "av1_vaapi", "vp9_vaapi"},
	}

	tests := []struct {
		codec    string
		expected string
	}{
		{"h264", "h264_vaapi"},
		{"hevc", "hevc_vaapi"},
		{"av1", "av1_vaapi"},
		{"vp9", "vp9_vaapi"},
	}

	for _, tt := range tests {
		t.Run(tt.codec, func(t *testing.T) {
			got := EncoderForCodec(tt.codec, hw)
			if got != tt.expected {
				t.Errorf("EncoderForCodec(%q) = %q, want %q", tt.codec, got, tt.expected)
			}
		})
	}
}

func TestDecoderForCodec(t *testing.T) {
	hw := HWProfile{
		Decoders: []string{"h264_cuvid", "hevc_cuvid"},
	}

	if got := DecoderForCodec("h264", hw); got != "h264_cuvid" {
		t.Errorf("DecoderForCodec(h264) = %q, want h264_cuvid", got)
	}
	if got := DecoderForCodec("av1", hw); got != "" {
		t.Errorf("DecoderForCodec(av1) = %q, want empty (no av1_cuvid)", got)
	}
}

func TestDecoderForCodec_QSV(t *testing.T) {
	hw := HWProfile{
		Decoders: []string{"h264_qsv", "hevc_qsv", "av1_qsv", "vp9_qsv"},
	}

	tests := []struct {
		codec    string
		expected string
	}{
		{"h264", "h264_qsv"},
		{"hevc", "hevc_qsv"},
		{"av1", "av1_qsv"},
		{"vp9", "vp9_qsv"},
	}

	for _, tt := range tests {
		t.Run(tt.codec, func(t *testing.T) {
			got := DecoderForCodec(tt.codec, hw)
			if got != tt.expected {
				t.Errorf("DecoderForCodec(%q) = %q, want %q", tt.codec, got, tt.expected)
			}
		})
	}
}
