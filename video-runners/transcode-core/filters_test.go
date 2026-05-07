package transcode

import (
	"strings"
	"testing"
)

func TestNeedsAdvancedFilters(t *testing.T) {
	tests := []struct {
		name string
		opts TranscodeOptions
		want bool
	}{
		{"empty", TranscodeOptions{}, false},
		{"subtitle only", TranscodeOptions{SubtitlePath: "/tmp/s.srt"}, true},
		{"watermark only", TranscodeOptions{WatermarkPath: "/tmp/w.png"}, true},
		{"tonemap only", TranscodeOptions{ToneMap: true}, true},
		{"all", TranscodeOptions{SubtitlePath: "/s.srt", WatermarkPath: "/w.png", ToneMap: true}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.opts.NeedsAdvancedFilters(); got != tt.want {
				t.Errorf("NeedsAdvancedFilters() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNeedsSoftwareDecode(t *testing.T) {
	tests := []struct {
		name string
		opts TranscodeOptions
		want bool
	}{
		{"empty", TranscodeOptions{}, false},
		{"subtitle", TranscodeOptions{SubtitlePath: "/s.srt"}, true},
		{"watermark", TranscodeOptions{WatermarkPath: "/w.png"}, true},
		{"tonemap only", TranscodeOptions{ToneMap: true}, false},
		{"subtitle+tonemap", TranscodeOptions{SubtitlePath: "/s.srt", ToneMap: true}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.opts.NeedsSoftwareDecode(); got != tt.want {
				t.Errorf("NeedsSoftwareDecode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildAdvancedFilterGraph_Subtitle(t *testing.T) {
	opts := TranscodeOptions{SubtitlePath: "/tmp/subs.srt"}
	hw := HWProfile{}
	probe := ProbeResult{Width: 1920, Height: 1080}

	filterType, filterStr := BuildAdvancedFilterGraph(opts, hw, probe, 0, 0)

	if filterType != "vf" {
		t.Errorf("filterType = %q, want vf", filterType)
	}
	if !strings.Contains(filterStr, "subtitles=") {
		t.Errorf("expected subtitles filter, got: %s", filterStr)
	}
	if !strings.Contains(filterStr, "/tmp/subs.srt") {
		t.Errorf("expected subtitle path, got: %s", filterStr)
	}
}

func TestBuildAdvancedFilterGraph_SubtitleWithScale(t *testing.T) {
	opts := TranscodeOptions{SubtitlePath: "/tmp/subs.srt"}
	hw := HWProfile{}
	probe := ProbeResult{Width: 3840, Height: 2160}

	filterType, filterStr := BuildAdvancedFilterGraph(opts, hw, probe, 1920, 1080)

	if filterType != "vf" {
		t.Errorf("filterType = %q, want vf", filterType)
	}
	if !strings.Contains(filterStr, "scale=1920:1080") {
		t.Errorf("expected scale filter, got: %s", filterStr)
	}
	if !strings.Contains(filterStr, "subtitles=") {
		t.Errorf("expected subtitles filter, got: %s", filterStr)
	}
}

func TestBuildAdvancedFilterGraph_Watermark(t *testing.T) {
	opts := TranscodeOptions{
		WatermarkPath:  "/tmp/logo.png",
		WatermarkPos:   "bottom-right",
		WatermarkScale: 0.1,
	}
	hw := HWProfile{}
	probe := ProbeResult{Width: 1920, Height: 1080}

	filterType, filterStr := BuildAdvancedFilterGraph(opts, hw, probe, 0, 0)

	if filterType != "filter_complex" {
		t.Errorf("filterType = %q, want filter_complex", filterType)
	}
	if !strings.Contains(filterStr, "[1:v]") {
		t.Errorf("expected [1:v] watermark input, got: %s", filterStr)
	}
	if !strings.Contains(filterStr, "overlay=") {
		t.Errorf("expected overlay filter, got: %s", filterStr)
	}
	if !strings.Contains(filterStr, "[out]") {
		t.Errorf("expected [out] output label, got: %s", filterStr)
	}
}

func TestBuildAdvancedFilterGraph_SubtitleAndWatermark(t *testing.T) {
	opts := TranscodeOptions{
		SubtitlePath:   "/tmp/subs.srt",
		WatermarkPath:  "/tmp/logo.png",
		WatermarkPos:   "top-left",
		WatermarkScale: 0.2,
	}
	hw := HWProfile{}
	probe := ProbeResult{Width: 1920, Height: 1080}

	filterType, filterStr := BuildAdvancedFilterGraph(opts, hw, probe, 0, 0)

	if filterType != "filter_complex" {
		t.Errorf("filterType = %q, want filter_complex", filterType)
	}
	if !strings.Contains(filterStr, "subtitles=") {
		t.Errorf("expected subtitles filter, got: %s", filterStr)
	}
	if !strings.Contains(filterStr, "overlay=") {
		t.Errorf("expected overlay filter, got: %s", filterStr)
	}
}

func TestBuildAdvancedFilterGraph_TonemapCPUFallback(t *testing.T) {
	opts := TranscodeOptions{ToneMap: true, SubtitlePath: "/tmp/subs.srt"}
	hw := HWProfile{} // no GPU
	probe := ProbeResult{
		Width:         3840,
		Height:        2160,
		ColorTransfer: "smpte2084",
	}

	filterType, filterStr := BuildAdvancedFilterGraph(opts, hw, probe, 1920, 1080)

	if filterType != "vf" {
		t.Errorf("filterType = %q, want vf", filterType)
	}
	// Should use CPU tonemap (zscale)
	if !strings.Contains(filterStr, "zscale=") {
		t.Errorf("expected zscale CPU tonemap, got: %s", filterStr)
	}
	if !strings.Contains(filterStr, "tonemap=") {
		t.Errorf("expected tonemap filter, got: %s", filterStr)
	}
	if !strings.Contains(filterStr, "format=yuv420p") {
		t.Errorf("expected format=yuv420p in CPU tonemap chain, got: %s", filterStr)
	}
}

func TestBuildAdvancedFilterGraph_HDRPlusSubtitle(t *testing.T) {
	opts := TranscodeOptions{
		ToneMap:      true,
		SubtitlePath: "/tmp/subs.srt",
	}
	hw := HWProfile{
		GPUName:  "RTX 4090",
		Vendor:   VendorNVIDIA,
		Encoders: []string{"h264_nvenc"},
		HWAccels: []string{"cuda"},
	}
	probe := ProbeResult{
		Width:         3840,
		Height:        2160,
		ColorTransfer: "smpte2084",
	}

	filterType, filterStr := BuildAdvancedFilterGraph(opts, hw, probe, 1920, 1080)

	if filterType != "vf" {
		t.Errorf("filterType = %q, want vf", filterType)
	}
	// Should use CPU tonemap since software decode is forced by subtitle
	if !strings.Contains(filterStr, "zscale=") {
		t.Errorf("expected CPU tonemap (zscale) when subtitle forces SW decode, got: %s", filterStr)
	}
	if !strings.Contains(filterStr, "subtitles=") {
		t.Errorf("expected subtitles filter, got: %s", filterStr)
	}
	if !strings.Contains(filterStr, "scale=") {
		t.Errorf("expected scale filter, got: %s", filterStr)
	}
}

func TestBuildAdvancedFilterGraph_TonemapNotHDR(t *testing.T) {
	opts := TranscodeOptions{ToneMap: true}
	hw := HWProfile{}
	probe := ProbeResult{Width: 1920, Height: 1080} // SDR content

	_, filterStr := BuildAdvancedFilterGraph(opts, hw, probe, 0, 0)

	// ToneMap requested but content is not HDR — no tonemap filter
	if strings.Contains(filterStr, "tonemap") || strings.Contains(filterStr, "zscale") {
		t.Errorf("should not have tonemap for SDR content, got: %s", filterStr)
	}
}

func TestBuildGPUTonemapFilter(t *testing.T) {
	tests := []struct {
		name   string
		hw     HWProfile
		expect string
	}{
		{
			"NVIDIA",
			HWProfile{Vendor: VendorNVIDIA},
			"tonemap_cuda",
		},
		{
			"AMD",
			HWProfile{Vendor: VendorAMD},
			"tonemap_vaapi",
		},
		{
			"Intel",
			HWProfile{Vendor: VendorIntel},
			"tonemap_opencl",
		},
		{
			"Software",
			HWProfile{},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildGPUTonemapFilter(tt.hw)
			if tt.expect == "" {
				if got != "" {
					t.Errorf("expected empty, got: %s", got)
				}
			} else {
				if !strings.Contains(got, tt.expect) {
					t.Errorf("expected %q in result, got: %s", tt.expect, got)
				}
			}
		})
	}
}

func TestWatermarkPositions(t *testing.T) {
	tests := []struct {
		pos     string
		expectX string
		expectY string
	}{
		{"top-left", "10", "10"},
		{"top-right", "W-w-10", "10"},
		{"bottom-left", "10", "H-h-10"},
		{"bottom-right", "W-w-10", "H-h-10"},
		{"center", "(W-w)/2", "(H-h)/2"},
		{"", "W-w-10", "H-h-10"}, // default is bottom-right
	}

	for _, tt := range tests {
		t.Run(tt.pos, func(t *testing.T) {
			x, y := watermarkPosition(tt.pos)
			if x != tt.expectX {
				t.Errorf("x = %q, want %q", x, tt.expectX)
			}
			if y != tt.expectY {
				t.Errorf("y = %q, want %q", y, tt.expectY)
			}
		})
	}
}

func TestBuildSubtitleFilter(t *testing.T) {
	filter := buildSubtitleFilter("/tmp/test.srt")
	if !strings.Contains(filter, "subtitles=") {
		t.Errorf("expected subtitles= in filter, got: %s", filter)
	}
	if !strings.Contains(filter, "/tmp/test.srt") {
		t.Errorf("expected path in filter, got: %s", filter)
	}
}

func TestBuildSubtitleFilter_SpecialChars(t *testing.T) {
	filter := buildSubtitleFilter("/tmp/my:file.srt")
	// Colon should be escaped
	if !strings.Contains(filter, "\\:") {
		t.Errorf("expected escaped colon in filter, got: %s", filter)
	}
}

func TestWatermarkScaleFilter(t *testing.T) {
	filter := watermarkScaleFilter(0.15)
	if !strings.Contains(filter, "scale=") {
		t.Errorf("expected scale= in filter, got: %s", filter)
	}
	if !strings.Contains(filter, "0.15") {
		t.Errorf("expected 0.15 scale factor, got: %s", filter)
	}
}

func TestBuildAdvancedFilterGraph_WatermarkDefaultScale(t *testing.T) {
	opts := TranscodeOptions{
		WatermarkPath: "/tmp/logo.png",
		// WatermarkScale not set, should default to 0.15
	}
	hw := HWProfile{}
	probe := ProbeResult{Width: 1920, Height: 1080}

	_, filterStr := BuildAdvancedFilterGraph(opts, hw, probe, 0, 0)

	if !strings.Contains(filterStr, "0.15") {
		t.Errorf("expected default watermark scale 0.15, got: %s", filterStr)
	}
}

func TestNeedsScale(t *testing.T) {
	tests := []struct {
		name    string
		targetW int
		targetH int
		probe   ProbeResult
		want    bool
	}{
		{"no target", 0, 0, ProbeResult{Width: 1920, Height: 1080}, false},
		{"same dims", 1920, 1080, ProbeResult{Width: 1920, Height: 1080}, false},
		{"different", 1280, 720, ProbeResult{Width: 1920, Height: 1080}, true},
		{"width only", 1280, 0, ProbeResult{Width: 1920, Height: 1080}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := needsScale(tt.targetW, tt.targetH, tt.probe); got != tt.want {
				t.Errorf("needsScale() = %v, want %v", got, tt.want)
			}
		})
	}
}
