package transcode

import (
	"strings"
	"testing"
)

func TestHLSRenditionCmd_WithGPU(t *testing.T) {
	hw := HWProfile{
		GPUName:  "RTX 4090",
		Vendor:   VendorNVIDIA,
		Encoders: []string{"h264_nvenc", "hevc_nvenc"},
		HWAccels: []string{"cuda"},
	}
	rendition := ABRRendition{
		Name: "1080p",
		Video: &ABRVideoSettings{
			Codec:      "h264",
			Width:      1920,
			Height:     1080,
			Bitrate:    "5M",
			MaxBitrate: "7.5M",
			Profile:    "high",
			Level:      "4.1",
		},
		Audio: ABRAudioSettings{
			Codec:    "aac",
			Bitrate:  "128k",
			Channels: 2,
		},
	}
	probe := ProbeResult{Width: 3840, Height: 2160}

	cmd := HLSRenditionCmd("/tmp/input.mp4", "/tmp/out/1080p", rendition, 6, hw, probe)
	args := strings.Join(cmd.Args, " ")

	// GPU flags
	if !strings.Contains(args, "-hwaccel cuda") {
		t.Error("expected -hwaccel cuda")
	}
	if !strings.Contains(args, "-c:v h264_nvenc") {
		t.Error("expected -c:v h264_nvenc")
	}
	if !strings.Contains(args, "-preset p4") {
		t.Error("expected -preset p4")
	}

	// Bitrate
	if !strings.Contains(args, "-b:v 5M") {
		t.Error("expected -b:v 5M")
	}
	if !strings.Contains(args, "-maxrate 7.5M") {
		t.Error("expected -maxrate 7.5M")
	}

	// Scale (4K input → 1080p output)
	if !strings.Contains(args, "scale_cuda=1920:1080") {
		t.Error("expected scale_cuda=1920:1080")
	}

	// Profile and level
	if !strings.Contains(args, "-profile:v high") {
		t.Error("expected -profile:v high")
	}
	if !strings.Contains(args, "-level 4.1") {
		t.Error("expected -level 4.1")
	}

	// Audio
	if !strings.Contains(args, "-c:a aac") {
		t.Error("expected -c:a aac")
	}
	if !strings.Contains(args, "-b:a 128k") {
		t.Error("expected -b:a 128k")
	}

	// HLS muxer flags
	if !strings.Contains(args, "-f hls") {
		t.Error("expected -f hls")
	}
	if !strings.Contains(args, "-hls_segment_type fmp4") {
		t.Error("expected -hls_segment_type fmp4")
	}
	if !strings.Contains(args, "-hls_flags single_file") {
		t.Error("expected -hls_flags single_file")
	}
	if !strings.Contains(args, "-hls_time 6") {
		t.Error("expected -hls_time 6")
	}
	if !strings.Contains(args, "-hls_playlist_type vod") {
		t.Error("expected -hls_playlist_type vod")
	}

	// Output paths
	if !strings.Contains(args, "/tmp/out/1080p/stream.mp4") {
		t.Error("expected stream.mp4 segment filename")
	}
	if !strings.Contains(args, "/tmp/out/1080p/playlist.m3u8") {
		t.Error("expected playlist.m3u8 output")
	}
}

func TestHLSRenditionCmd_WithoutGPU(t *testing.T) {
	hw := HWProfile{}
	rendition := ABRRendition{
		Name: "720p",
		Video: &ABRVideoSettings{
			Codec:   "h264",
			Width:   1280,
			Height:  720,
			Bitrate: "2.5M",
			PixFmt:  "yuv420p",
		},
		Audio: ABRAudioSettings{Codec: "aac", Bitrate: "96k", Channels: 2},
	}
	probe := ProbeResult{Width: 1920, Height: 1080}

	cmd := HLSRenditionCmd("/tmp/input.mp4", "/tmp/out/720p", rendition, 6, hw, probe)
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "-c:v libx264") {
		t.Error("expected -c:v libx264 without GPU")
	}
	if strings.Contains(args, "-hwaccel") {
		t.Error("should not have -hwaccel without GPU")
	}
	if !strings.Contains(args, "scale=1280:720") {
		t.Error("expected scale=1280:720 for software")
	}
	if !strings.Contains(args, "-pix_fmt yuv420p") {
		t.Error("expected -pix_fmt yuv420p for software")
	}
}

func TestHLSRenditionCmd_AudioOnly(t *testing.T) {
	hw := HWProfile{GPUName: "RTX 4090", Vendor: VendorNVIDIA, Encoders: []string{"h264_nvenc"}, HWAccels: []string{"cuda"}}
	rendition := ABRRendition{
		Name:  "audio-only",
		Video: nil,
		Audio: ABRAudioSettings{Codec: "aac", Bitrate: "64k", Channels: 2},
	}
	probe := ProbeResult{Width: 1920, Height: 1080}

	cmd := HLSRenditionCmd("/tmp/input.mp4", "/tmp/out/audio", rendition, 6, hw, probe)
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "-vn") {
		t.Error("expected -vn for audio-only")
	}
	if strings.Contains(args, "-c:v") {
		t.Error("should not have -c:v for audio-only")
	}
	if !strings.Contains(args, "-c:a aac") {
		t.Error("expected -c:a aac")
	}
}

func TestHLSRenditionCmd_SameDimensions(t *testing.T) {
	hw := HWProfile{GPUName: "RTX 4090", Vendor: VendorNVIDIA, Encoders: []string{"h264_nvenc"}, HWAccels: []string{"cuda"}}
	rendition := ABRRendition{
		Name:  "1080p",
		Video: &ABRVideoSettings{Codec: "h264", Width: 1920, Height: 1080, Bitrate: "5M"},
		Audio: ABRAudioSettings{Codec: "aac", Bitrate: "128k", Channels: 2},
	}
	probe := ProbeResult{Width: 1920, Height: 1080}

	cmd := HLSRenditionCmd("/tmp/input.mp4", "/tmp/out/1080p", rendition, 6, hw, probe)
	args := strings.Join(cmd.Args, " ")

	if strings.Contains(args, "scale") {
		t.Error("should not have scale filter when dimensions match")
	}
}

func TestHLSRenditionCmd_WithIntelQSV(t *testing.T) {
	hw := HWProfile{
		GPUName:    "Intel(R) Arc A770",
		Vendor:     VendorIntel,
		DevicePath: "/dev/dri/renderD128",
		Encoders:   []string{"h264_qsv", "hevc_qsv"},
		HWAccels:   []string{"qsv", "vaapi"},
	}
	rendition := ABRRendition{
		Name: "1080p",
		Video: &ABRVideoSettings{
			Codec:      "h264",
			Width:      1920,
			Height:     1080,
			Bitrate:    "5M",
			MaxBitrate: "7.5M",
			Profile:    "high",
			Level:      "4.1",
		},
		Audio: ABRAudioSettings{Codec: "aac", Bitrate: "128k", Channels: 2},
	}
	probe := ProbeResult{Width: 3840, Height: 2160}

	cmd := HLSRenditionCmd("/tmp/input.mp4", "/tmp/out/1080p", rendition, 6, hw, probe)
	args := strings.Join(cmd.Args, " ")

	// QSV hwaccel
	if !strings.Contains(args, "-hwaccel qsv") {
		t.Errorf("expected -hwaccel qsv, got: %s", args)
	}
	if !strings.Contains(args, "-hwaccel_output_format qsv") {
		t.Errorf("expected -hwaccel_output_format qsv, got: %s", args)
	}
	// QSV encoder
	if !strings.Contains(args, "-c:v h264_qsv") {
		t.Errorf("expected -c:v h264_qsv, got: %s", args)
	}
	// QSV tuning
	if !strings.Contains(args, "-preset medium") {
		t.Errorf("expected -preset medium for QSV, got: %s", args)
	}
	if !strings.Contains(args, "-look_ahead 1") {
		t.Errorf("expected -look_ahead 1 for QSV, got: %s", args)
	}
	// QSV scale filter
	if !strings.Contains(args, "scale_qsv=w=1920:h=1080") {
		t.Errorf("expected scale_qsv=w=1920:h=1080, got: %s", args)
	}
	// GPU-managed pix_fmt
	if strings.Contains(args, "-pix_fmt") {
		t.Error("should not have -pix_fmt for Intel QSV")
	}
	// HLS muxer flags still present
	if !strings.Contains(args, "-f hls") {
		t.Error("expected -f hls")
	}
}

func TestHLSRenditionCmd_WithAMDVAAPI(t *testing.T) {
	hw := HWProfile{
		GPUName:    "AMD Radeon RX 7900 XTX",
		Vendor:     VendorAMD,
		DevicePath: "/dev/dri/renderD128",
		Encoders:   []string{"h264_vaapi", "hevc_vaapi"},
		HWAccels:   []string{"vaapi"},
	}
	rendition := ABRRendition{
		Name: "1080p",
		Video: &ABRVideoSettings{
			Codec:      "h264",
			Width:      1920,
			Height:     1080,
			Bitrate:    "5M",
			MaxBitrate: "7.5M",
			Profile:    "high",
			Level:      "4.1",
		},
		Audio: ABRAudioSettings{Codec: "aac", Bitrate: "128k", Channels: 2},
	}
	probe := ProbeResult{Width: 3840, Height: 2160}

	cmd := HLSRenditionCmd("/tmp/input.mp4", "/tmp/out/1080p", rendition, 6, hw, probe)
	args := strings.Join(cmd.Args, " ")

	// VAAPI hwaccel
	if !strings.Contains(args, "-hwaccel vaapi") {
		t.Errorf("expected -hwaccel vaapi, got: %s", args)
	}
	if !strings.Contains(args, "-hwaccel_device /dev/dri/renderD128") {
		t.Errorf("expected -hwaccel_device, got: %s", args)
	}
	// VAAPI encoder
	if !strings.Contains(args, "-c:v h264_vaapi") {
		t.Errorf("expected -c:v h264_vaapi, got: %s", args)
	}
	// VAAPI tuning
	if !strings.Contains(args, "-rc_mode VBR") {
		t.Errorf("expected -rc_mode VBR, got: %s", args)
	}
	// VAAPI scale filter with hwupload
	if !strings.Contains(args, "format=nv12,hwupload,scale_vaapi=w=1920:h=1080") {
		t.Errorf("expected format=nv12,hwupload,scale_vaapi=w=1920:h=1080, got: %s", args)
	}
	// GPU-managed pix_fmt
	if strings.Contains(args, "-pix_fmt") {
		t.Error("should not have -pix_fmt for AMD VAAPI")
	}
}

func TestGenerateMasterPlaylist(t *testing.T) {
	renditions := []ABRRendition{
		{
			Name:  "1080p",
			Video: &ABRVideoSettings{Codec: "h264", Width: 1920, Height: 1080, Bitrate: "5M", Profile: "high", Level: "4.1"},
			Audio: ABRAudioSettings{Codec: "aac", Bitrate: "128k"},
		},
		{
			Name:  "720p",
			Video: &ABRVideoSettings{Codec: "h264", Width: 1280, Height: 720, Bitrate: "2.5M", Profile: "high", Level: "3.1"},
			Audio: ABRAudioSettings{Codec: "aac", Bitrate: "96k"},
		},
		{
			Name:  "audio-only",
			Video: nil,
			Audio: ABRAudioSettings{Codec: "aac", Bitrate: "64k"},
		},
	}

	paths := map[string]string{
		"1080p":      "1080p/playlist.m3u8",
		"720p":       "720p/playlist.m3u8",
		"audio-only": "audio-only/playlist.m3u8",
	}

	manifest := GenerateMasterPlaylist(renditions, paths)

	if !strings.Contains(manifest, "#EXTM3U") {
		t.Error("missing #EXTM3U")
	}
	if !strings.Contains(manifest, "#EXT-X-VERSION:7") {
		t.Error("missing #EXT-X-VERSION:7")
	}

	// 1080p: 5M + 128k = 5128000
	if !strings.Contains(manifest, "BANDWIDTH=5128000") {
		t.Errorf("expected BANDWIDTH=5128000 for 1080p, got:\n%s", manifest)
	}
	if !strings.Contains(manifest, "RESOLUTION=1920x1080") {
		t.Error("missing RESOLUTION=1920x1080")
	}
	if !strings.Contains(manifest, "avc1.640029") {
		t.Error("missing H.264 High 4.1 codec string")
	}
	if !strings.Contains(manifest, "1080p/playlist.m3u8") {
		t.Error("missing 1080p playlist path")
	}

	// 720p: 2.5M + 96k = 2596000
	if !strings.Contains(manifest, "BANDWIDTH=2596000") {
		t.Errorf("expected BANDWIDTH=2596000 for 720p, got:\n%s", manifest)
	}
	if !strings.Contains(manifest, "avc1.64001F") {
		t.Error("missing H.264 High 3.1 codec string")
	}

	// Audio-only: 64k = 64000
	if !strings.Contains(manifest, "BANDWIDTH=64000") {
		t.Errorf("expected BANDWIDTH=64000 for audio-only, got:\n%s", manifest)
	}
	if !strings.Contains(manifest, "audio-only/playlist.m3u8") {
		t.Error("missing audio-only playlist path")
	}
}

func TestParseBandwidth(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"5M", 5000000},
		{"2.5M", 2500000},
		{"600k", 600000},
		{"128k", 128000},
		{"64k", 64000},
		{"1500k", 1500000},
		{"1000000", 1000000},
		{"", 0},
		{"invalid", 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseBandwidth(tt.input)
			if got != tt.expected {
				t.Errorf("parseBandwidth(%q) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestHlsCodecString(t *testing.T) {
	tests := []struct {
		name     string
		video    string
		profile  string
		level    string
		audio    string
		expected string
	}{
		{"H264 High 4.1 + AAC", "h264", "high", "4.1", "aac", "avc1.640029,mp4a.40.2"},
		{"H264 Main 3.0 + AAC", "h264", "main", "3.0", "aac", "avc1.4D001E,mp4a.40.2"},
		{"H264 Baseline 3.1 + AAC", "h264", "baseline", "3.1", "aac", "avc1.42001F,mp4a.40.2"},
		{"HEVC + AAC", "hevc", "", "", "aac", "hvc1.1.6.L120.B0,mp4a.40.2"},
		{"AV1 + AAC", "av1", "", "", "aac", "av01.0.08M.08,mp4a.40.2"},
		{"H264 + Opus", "h264", "high", "4.1", "opus", "avc1.640029,Opus"},
		{"H264 default level", "h264", "high", "", "aac", "avc1.64001F,mp4a.40.2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hlsCodecString(tt.video, tt.profile, tt.level, tt.audio)
			if got != tt.expected {
				t.Errorf("hlsCodecString() = %q, want %q", got, tt.expected)
			}
		})
	}
}
