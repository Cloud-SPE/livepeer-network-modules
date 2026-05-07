package transcode

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// HLSRenditionCmd builds an ffmpeg command that encodes one rendition as fMP4
// byte-range HLS. The command writes two files into outputDir:
//   - playlist.m3u8 (HLS playlist with EXT-X-BYTERANGE directives)
//   - stream.mp4    (single fragmented MP4 file)
func HLSRenditionCmd(
	inputPath string,
	outputDir string,
	rendition ABRRendition,
	segmentDuration int,
	hw HWProfile,
	probe ProbeResult,
) *exec.Cmd {
	args := []string{"-y"}

	// Hardware acceleration input
	args = append(args, buildHWAccelInputArgs(hw)...)

	args = append(args, "-i", inputPath)

	// Video encoding (or skip for audio-only)
	if rendition.Video != nil {
		args = append(args, buildHLSVideoArgs(rendition, hw)...)

		if filters := buildHLSFilterGraph(rendition, hw, probe); filters != "" {
			args = append(args, "-vf", filters)
		}
	} else {
		args = append(args, "-vn")
	}

	// Audio encoding
	args = append(args, buildHLSAudioArgs(rendition)...)

	// HLS muxer options for fMP4 byte-range
	args = append(args,
		"-f", "hls",
		"-hls_segment_type", "fmp4",
		"-hls_flags", "single_file",
		"-hls_time", strconv.Itoa(segmentDuration),
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(outputDir, "stream.mp4"),
	)

	// Output playlist path
	args = append(args, filepath.Join(outputDir, "playlist.m3u8"))

	return exec.Command("ffmpeg", args...)
}

// buildHLSVideoArgs constructs video encoding arguments for an HLS rendition.
func buildHLSVideoArgs(rendition ABRRendition, hw HWProfile) []string {
	v := rendition.Video
	if v == nil {
		return nil
	}

	args := []string{}

	encoder := EncoderForCodec(v.Codec, hw)
	args = append(args, "-c:v", encoder)

	// Vendor-specific tuning
	args = append(args, buildEncoderTuningArgs(encoder)...)

	if v.Bitrate != "" {
		args = append(args, "-b:v", v.Bitrate)
	}
	if v.MaxBitrate != "" {
		args = append(args, "-maxrate", v.MaxBitrate)
	}
	if v.BufSize != "" {
		args = append(args, "-bufsize", v.BufSize)
	}

	// Pixel format — GPU vendors manage this internally
	if v.PixFmt != "" && !isHWManagedPixFmt(hw) {
		args = append(args, "-pix_fmt", v.PixFmt)
	}

	if v.Profile != "" {
		args = append(args, "-profile:v", v.Profile)
	}

	if v.Level != "" {
		args = append(args, "-level", v.Level)
	}

	return args
}

// buildHLSAudioArgs constructs audio encoding arguments for an HLS rendition.
func buildHLSAudioArgs(rendition ABRRendition) []string {
	a := rendition.Audio
	args := []string{}

	if a.Codec != "" {
		args = append(args, "-c:a", a.Codec)
	} else {
		args = append(args, "-c:a", "aac")
	}

	if a.Bitrate != "" {
		args = append(args, "-b:a", a.Bitrate)
	}

	if a.Channels > 0 {
		args = append(args, "-ac", strconv.Itoa(a.Channels))
	}

	if a.SampleRate > 0 {
		args = append(args, "-ar", strconv.Itoa(a.SampleRate))
	}

	return args
}

// buildHLSFilterGraph constructs the filter graph for an HLS rendition.
func buildHLSFilterGraph(rendition ABRRendition, hw HWProfile, probe ProbeResult) string {
	v := rendition.Video
	if v == nil {
		return ""
	}

	if v.Width <= 0 || v.Height <= 0 {
		return ""
	}
	if v.Width == probe.Width && v.Height == probe.Height {
		return ""
	}

	filters := []string{}

	switch hw.Vendor {
	case VendorNVIDIA:
		// Upload software-decoded frames to GPU before scale_cuda.
		// This handles the common case where CUDA hwaccel decode fails
		// (e.g. too many decode surfaces) and ffmpeg falls back to CPU decode.
		if hw.HasHWAccel("cuda") {
			filters = append(filters, "hwupload_cuda")
		}
	case VendorAMD:
		// AMD VAAPI requires hwupload before scale
		if hw.HasHWAccel("vaapi") {
			filters = append(filters, "format=nv12", "hwupload")
		}
	}

	filters = append(filters, buildScaleFilter(v.Width, v.Height, hw))

	return strings.Join(filters, ",")
}

// GenerateMasterPlaylist generates a master.m3u8 content string that references
// per-rendition playlists. playlistPaths maps rendition names to relative paths
// (e.g., "1080p" -> "1080p/playlist.m3u8").
func GenerateMasterPlaylist(renditions []ABRRendition, playlistPaths map[string]string) string {
	var sb strings.Builder

	sb.WriteString("#EXTM3U\n")
	sb.WriteString("#EXT-X-VERSION:7\n")
	sb.WriteString("\n")

	for _, r := range renditions {
		path, ok := playlistPaths[r.Name]
		if !ok {
			continue
		}

		if r.Video != nil {
			bandwidth := parseBandwidth(r.Video.Bitrate) + parseBandwidth(r.Audio.Bitrate)

			sb.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d",
				bandwidth, r.Video.Width, r.Video.Height))

			codecs := hlsCodecString(r.Video.Codec, r.Video.Profile, r.Video.Level, r.Audio.Codec)
			if codecs != "" {
				sb.WriteString(fmt.Sprintf(",CODECS=\"%s\"", codecs))
			}

			sb.WriteString("\n")
		} else {
			// Audio-only rendition
			bandwidth := parseBandwidth(r.Audio.Bitrate)
			sb.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d\n", bandwidth))
		}

		sb.WriteString(path + "\n")
	}

	return sb.String()
}

// parseBandwidth converts a bitrate string like "5M", "2.5M", "600k" to bits per second.
func parseBandwidth(bitrate string) int {
	if bitrate == "" {
		return 0
	}
	bitrate = strings.TrimSpace(bitrate)

	var multiplier float64
	var numStr string

	switch {
	case strings.HasSuffix(bitrate, "M") || strings.HasSuffix(bitrate, "m"):
		multiplier = 1_000_000
		numStr = bitrate[:len(bitrate)-1]
	case strings.HasSuffix(bitrate, "K") || strings.HasSuffix(bitrate, "k"):
		multiplier = 1_000
		numStr = bitrate[:len(bitrate)-1]
	default:
		if v, err := strconv.Atoi(bitrate); err == nil {
			return v
		}
		return 0
	}

	if v, err := strconv.ParseFloat(numStr, 64); err == nil {
		return int(v * multiplier)
	}
	return 0
}

// hlsCodecString returns the RFC 6381 codec string for HLS master playlist CODECS attribute.
func hlsCodecString(videoCodec, profile, level, audioCodec string) string {
	var parts []string

	switch strings.ToLower(videoCodec) {
	case "h264", "avc":
		profileHex := "6400" // High
		switch strings.ToLower(profile) {
		case "baseline":
			profileHex = "4200"
		case "main":
			profileHex = "4D00"
		case "high":
			profileHex = "6400"
		}
		levelHex := h264LevelToHex(level)
		parts = append(parts, fmt.Sprintf("avc1.%s%s", profileHex, levelHex))
	case "h265", "hevc":
		parts = append(parts, "hvc1.1.6.L120.B0")
	case "av1":
		parts = append(parts, "av01.0.08M.08")
	}

	switch strings.ToLower(audioCodec) {
	case "aac":
		parts = append(parts, "mp4a.40.2")
	case "opus":
		parts = append(parts, "Opus")
	}

	return strings.Join(parts, ",")
}

// h264LevelToHex converts an H.264 level string like "4.1" to hex representation.
func h264LevelToHex(level string) string {
	switch level {
	case "3.0":
		return "1E"
	case "3.1":
		return "1F"
	case "4.0":
		return "28"
	case "4.1":
		return "29"
	case "4.2":
		return "2A"
	case "5.0":
		return "32"
	case "5.1":
		return "33"
	case "5.2":
		return "34"
	default:
		return "1F" // default to 3.1
	}
}
