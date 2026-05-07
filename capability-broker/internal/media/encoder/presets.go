package encoder

import "fmt"

// Profile names recognised by host-config.yaml `backend.profile`.
const (
	ProfilePassthrough     = "passthrough"
	ProfileLibx264_1080p   = "h264-live-1080p-libx264"
	ProfileNVENC_1080p     = "h264-live-1080p-nvenc"
	ProfileQSV_1080p       = "h264-live-1080p-qsv"
	ProfileVAAPI_1080p     = "h264-live-1080p-vaapi"
)

// Rung describes one ABR ladder step. The 5-rung 1080p ladder uses the
// same Resolution / Bitrate / H.264 profile across builders; only the
// codec name + builder-specific args differ.
type Rung struct {
	Name        string
	Width       int
	Height      int
	BitrateKbps int
	H264Profile string
}

// FiveRungLadder is the canonical 5-rung H.264 ABR ladder shared by
// the four GPU/CPU encoder profiles. Source: Apple HLS Authoring Spec
// + Mux published encoder recommendations + go-livepeer's VideoProfile
// constants (see plan §5.3 table).
var FiveRungLadder = []Rung{
	{Name: "240p", Width: 426, Height: 240, BitrateKbps: 400, H264Profile: "baseline"},
	{Name: "360p", Width: 640, Height: 360, BitrateKbps: 800, H264Profile: "baseline"},
	{Name: "480p", Width: 854, Height: 480, BitrateKbps: 1400, H264Profile: "main"},
	{Name: "720p", Width: 1280, Height: 720, BitrateKbps: 2800, H264Profile: "main"},
	{Name: "1080p", Width: 1920, Height: 1080, BitrateKbps: 5000, H264Profile: "high"},
}

// HLSOptions captures the muxer flags shared by passthrough and ladder
// profiles. Filled in by the caller from --hls-* flags; the builder
// renders them into FFmpeg argv.
type HLSOptions struct {
	// Legacy flips to mpegts HLS v3 (~12-24s glass-to-glass).
	Legacy bool
	// SegmentDuration in seconds (`-hls_time`). Default 2 for
	// LL-HLS; 6 for legacy.
	SegmentDuration int
	// PartDuration is the LL-HLS `#EXT-X-PART` duration in seconds
	// (e.g. 0.333). Ignored when Legacy is true.
	PartDuration float64
	// PlaylistWindow is `-hls_list_size` (default 4 LL-HLS, 5 legacy).
	PlaylistWindow int
	// ScratchDir is the per-session output directory.
	ScratchDir string
}

// PresetInput bundles the per-session knobs the builder needs.
type PresetInput struct {
	Profile string
	Codec   Codec
	HLS     HLSOptions
}

// BuildArgs renders the FFmpeg argv for the named profile. Pure
// function (no IO) so it's unit-testable.
func BuildArgs(in PresetInput) ([]string, error) {
	if in.HLS.ScratchDir == "" {
		return nil, fmt.Errorf("encoder: HLSOptions.ScratchDir is required")
	}
	switch in.Profile {
	case ProfilePassthrough:
		return buildPassthroughArgs(in.HLS), nil
	case ProfileLibx264_1080p:
		return buildLadderArgs(in.HLS, CodecLibx264, FiveRungLadder), nil
	case ProfileNVENC_1080p:
		return buildLadderArgs(in.HLS, CodecNVENC, FiveRungLadder), nil
	case ProfileQSV_1080p:
		return buildLadderArgs(in.HLS, CodecQSV, FiveRungLadder), nil
	case ProfileVAAPI_1080p:
		return buildLadderArgs(in.HLS, CodecVAAPI, FiveRungLadder), nil
	}
	return nil, fmt.Errorf("encoder: unknown profile %q", in.Profile)
}

// MatchesCodec reports whether a profile is compatible with the
// probed encoder. passthrough and libx264 work everywhere; the GPU
// profiles require their codec.
func MatchesCodec(profile string, c Codec) bool {
	switch profile {
	case ProfilePassthrough, ProfileLibx264_1080p:
		return true
	case ProfileNVENC_1080p:
		return c == CodecNVENC
	case ProfileQSV_1080p:
		return c == CodecQSV
	case ProfileVAAPI_1080p:
		return c == CodecVAAPI
	}
	return false
}
