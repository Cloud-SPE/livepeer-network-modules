package transcode

import (
	"os/exec"
	"strconv"
	"strings"
)

// LiveTranscodeParams holds parameters for live transcoding.
type LiveTranscodeParams struct {
	VideoCodec   string // h264, hevc, av1
	Width        int    // target width (0 = keep)
	Height       int    // target height (0 = keep)
	Bitrate      string // "4M", "2500k"
	MaxRate      string // VBR ceiling
	BufSize      string // VBV buffer
	FPS          int    // target fps (0 = keep)
	AudioCodec   string // "aac", "copy"
	AudioBitrate string // "128k"
}

// LiveTranscodeCmd builds an ffmpeg command for live MPEG-TS pipe I/O.
// Input: pipe:0 (stdin), Output: pipe:1 (stdout).
// Reuses internal helpers for hwaccel, encoder selection, scaling, and tuning.
func LiveTranscodeCmd(params LiveTranscodeParams, hw HWProfile) *exec.Cmd {
	args := []string{"-y"}

	// Low-latency input flags
	args = append(args, "-fflags", "+nobuffer", "-flags", "+low_delay")

	// Hardware acceleration input
	args = append(args, buildHWAccelInputArgs(hw)...)

	// MPEG-TS pipe input
	args = append(args, "-f", "mpegts", "-i", "pipe:0")

	// Video encoding
	encoder := EncoderForCodec(params.VideoCodec, hw)
	args = append(args, "-c:v", encoder)

	// Vendor-specific tuning
	args = append(args, buildEncoderTuningArgs(encoder)...)

	// Bitrate
	if params.Bitrate != "" {
		args = append(args, "-b:v", params.Bitrate)
	}
	if params.MaxRate != "" {
		args = append(args, "-maxrate", params.MaxRate)
	}
	if params.BufSize != "" {
		args = append(args, "-bufsize", params.BufSize)
	}

	// FPS override
	if params.FPS > 0 {
		args = append(args, "-r", strconv.Itoa(params.FPS))
	}

	// Scale filter (only when dimensions are specified)
	if params.Width > 0 || params.Height > 0 {
		var filters []string
		// AMD VAAPI requires hwupload before scale
		if hw.Vendor == VendorAMD && hw.HasHWAccel("vaapi") {
			filters = append(filters, "format=nv12", "hwupload")
		}
		filters = append(filters, buildScaleFilter(params.Width, params.Height, hw))
		args = append(args, "-vf", strings.Join(filters, ","))
	}

	// Audio encoding
	audioCodec := params.AudioCodec
	if audioCodec == "" {
		audioCodec = "aac"
	}
	args = append(args, "-c:a", audioCodec)
	if audioCodec != "copy" && params.AudioBitrate != "" {
		args = append(args, "-b:a", params.AudioBitrate)
	}

	// MPEG-TS pipe output (no -movflags, streaming format)
	args = append(args, "-f", "mpegts", "pipe:1")

	return exec.Command("ffmpeg", args...)
}
