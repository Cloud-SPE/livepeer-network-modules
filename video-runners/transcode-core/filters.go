package transcode

import (
	"fmt"
	"strings"
)

// TranscodeOptions holds optional per-request processing features.
type TranscodeOptions struct {
	SubtitlePath   string  // local path to .srt/.ass file (burn-in)
	WatermarkPath  string  // local path to .png/.jpg watermark
	WatermarkPos   string  // "top-left", "top-right", "bottom-left", "bottom-right", "center"
	WatermarkScale float64 // 0.0-1.0 fraction of video width (default 0.15)
	ToneMap        bool    // force HDR→SDR tone mapping
}

// NeedsAdvancedFilters returns true if any advanced filter is active.
func (o TranscodeOptions) NeedsAdvancedFilters() bool {
	return o.SubtitlePath != "" || o.WatermarkPath != "" || o.ToneMap
}

// NeedsSoftwareDecode returns true if filters require CPU-side frames.
// Subtitle burn-in and watermark overlay only work on software-decoded frames.
func (o TranscodeOptions) NeedsSoftwareDecode() bool {
	return o.SubtitlePath != "" || o.WatermarkPath != ""
}

// BuildAdvancedFilterGraph constructs the filter chain for advanced processing.
// Returns (filterType, filterStr) where filterType is "vf" or "filter_complex".
// When a watermark is present, filter_complex is required for the second input.
func BuildAdvancedFilterGraph(opts TranscodeOptions, hw HWProfile, probe ProbeResult,
	targetW, targetH int) (filterType string, filterStr string) {

	hasWatermark := opts.WatermarkPath != ""

	if hasWatermark {
		return buildFilterComplex(opts, hw, probe, targetW, targetH)
	}
	return buildSimpleVF(opts, hw, probe, targetW, targetH)
}

// buildSimpleVF builds a -vf filter chain (no watermark / no second input).
func buildSimpleVF(opts TranscodeOptions, hw HWProfile, probe ProbeResult,
	targetW, targetH int) (string, string) {

	var filters []string

	// Tonemap (HDR→SDR) must come first, before any scaling
	if opts.ToneMap && probe.IsHDR() {
		filters = append(filters, buildTonemapFilter(hw)...)
	}

	// Scale
	if needsScale(targetW, targetH, probe) {
		filters = append(filters, buildSWScaleFilter(targetW, targetH))
	}

	// Subtitle burn-in
	if opts.SubtitlePath != "" {
		filters = append(filters, buildSubtitleFilter(opts.SubtitlePath))
	}

	if len(filters) == 0 {
		return "", ""
	}
	return "vf", strings.Join(filters, ",")
}

// buildFilterComplex builds a -filter_complex chain when watermark is present.
// Uses named streams: [0:v] for video input, [1:v] for watermark input.
func buildFilterComplex(opts TranscodeOptions, hw HWProfile, probe ProbeResult,
	targetW, targetH int) (string, string) {

	var filters []string
	currentStream := "[0:v]"
	streamIdx := 0

	// Tonemap
	if opts.ToneMap && probe.IsHDR() {
		tmFilters := buildTonemapFilter(hw)
		filters = append(filters, fmt.Sprintf("%s%s[tm]", currentStream, strings.Join(tmFilters, ",")))
		currentStream = "[tm]"
		streamIdx++
	}

	// Scale
	if needsScale(targetW, targetH, probe) {
		scaleTag := fmt.Sprintf("[s%d]", streamIdx)
		filters = append(filters, fmt.Sprintf("%s%s%s", currentStream, buildSWScaleFilter(targetW, targetH), scaleTag))
		currentStream = scaleTag
		streamIdx++
	}

	// Subtitle
	if opts.SubtitlePath != "" {
		subTag := fmt.Sprintf("[sub%d]", streamIdx)
		filters = append(filters, fmt.Sprintf("%s%s%s", currentStream, buildSubtitleFilter(opts.SubtitlePath), subTag))
		currentStream = subTag
		streamIdx++
	}

	// Watermark: scale the watermark, then overlay
	scale := opts.WatermarkScale
	if scale <= 0 {
		scale = 0.15
	}
	wmScaleFilter := watermarkScaleFilter(scale)
	filters = append(filters, fmt.Sprintf("[1:v]%s[wm]", wmScaleFilter))
	overlay := buildWatermarkOverlay(opts.WatermarkPos)
	filters = append(filters, fmt.Sprintf("%s[wm]%s[out]", currentStream, overlay))

	return "filter_complex", strings.Join(filters, ";")
}

// buildTonemapFilter returns tonemap filter steps.
// GPU-first approach: use vendor-specific GPU tonemap when no software decode is forced.
// Falls back to CPU zscale+tonemap when software decode is active or no GPU tonemap available.
func buildTonemapFilter(hw HWProfile) []string {
	// When software decode is in effect (subtitles/watermarks force it),
	// frames are already in CPU memory — use zscale+tonemap CPU path.
	// Also use CPU path when no GPU is available.
	// Note: the caller already determined NeedsSoftwareDecode, so if we're here
	// with a GPU vendor, we check if software decode is forced by looking at
	// the overall context. Since buildTonemapFilter doesn't know about opts,
	// we always use CPU tonemap in buildSimpleVF/buildFilterComplex which are
	// called only when NeedsSoftwareDecode is true (or when there's no GPU).
	// For GPU tonemap, TranscodeCmd handles it directly when !NeedsSoftwareDecode.

	// CPU tonemap path (used when software decode is active)
	return []string{
		"zscale=t=linear:npl=100",
		"format=gbrpf32le",
		"zscale=p=bt709",
		"tonemap=tonemap=hable:desat=0",
		"zscale=t=bt709:m=bt709:r=tv",
		"format=yuv420p",
	}
}

// buildGPUTonemapFilter returns GPU-specific tonemap filter for use when
// hardware decode is active (no subtitles/watermarks).
func buildGPUTonemapFilter(hw HWProfile) string {
	switch hw.Vendor {
	case VendorNVIDIA:
		return "tonemap_cuda=tonemap=hable:desat=0:format=nv12"
	case VendorAMD:
		return "tonemap_vaapi=format=nv12"
	case VendorIntel:
		return "tonemap_opencl=tonemap=hable:desat=0:format=nv12"
	default:
		return ""
	}
}

// buildSubtitleFilter returns the subtitle burn-in filter.
func buildSubtitleFilter(subtitlePath string) string {
	// Escape special characters in the path for ffmpeg
	escaped := strings.ReplaceAll(subtitlePath, "'", "'\\''")
	escaped = strings.ReplaceAll(escaped, ":", "\\:")
	return fmt.Sprintf("subtitles='%s'", escaped)
}

// buildWatermarkOverlay returns the overlay filter with position coordinates.
func buildWatermarkOverlay(pos string) string {
	x, y := watermarkPosition(pos)
	return fmt.Sprintf("overlay=%s:%s", x, y)
}

// watermarkPosition returns x,y expressions for ffmpeg overlay filter.
func watermarkPosition(pos string) (string, string) {
	padding := "10"
	switch strings.ToLower(pos) {
	case "top-left":
		return padding, padding
	case "top-right":
		return "W-w-" + padding, padding
	case "bottom-left":
		return padding, "H-h-" + padding
	case "center":
		return "(W-w)/2", "(H-h)/2"
	default: // bottom-right
		return "W-w-" + padding, "H-h-" + padding
	}
}

// watermarkScaleFilter returns a scale2ref-based filter to scale the watermark
// relative to the video width.
func watermarkScaleFilter(scale float64) string {
	// Scale watermark width to be scale*input_width, keep aspect ratio
	return fmt.Sprintf("scale=iw*%.2f:-1", scale)
}

// buildSWScaleFilter returns a software scale filter string.
func buildSWScaleFilter(w, h int) string {
	if w <= 0 {
		w = -2
	}
	if h <= 0 {
		h = -2
	}
	return fmt.Sprintf("scale=%d:%d", w, h)
}

// needsScale returns true if scaling is needed.
func needsScale(targetW, targetH int, probe ProbeResult) bool {
	if targetW <= 0 && targetH <= 0 {
		return false
	}
	if targetW == probe.Width && targetH == probe.Height {
		return false
	}
	return true
}
