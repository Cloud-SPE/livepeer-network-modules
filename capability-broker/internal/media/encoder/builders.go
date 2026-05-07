package encoder

import (
	"fmt"
	"path/filepath"
	"strconv"
)

// buildPassthroughArgs assembles the FFmpeg argv for the
// CI-smoke / dev passthrough profile: copy the input streams and mux
// to LL-HLS without re-encoding.
func buildPassthroughArgs(h HLSOptions) []string {
	h = withHLSDefaults(h)
	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-progress", "pipe:2",
		"-f", "flv",
		"-i", "pipe:0",
		"-c:v", "copy",
		"-c:a", "copy",
	}
	args = append(args, hlsMuxerArgs(h, "")...)
	return args
}

// buildLadderArgs renders argv for a 5-rung ladder using the given
// codec. Audio is encoded once and mapped into every variant
// playlist.
func buildLadderArgs(h HLSOptions, c Codec, rungs []Rung) []string {
	h = withHLSDefaults(h)

	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-progress", "pipe:2",
	}
	if c == CodecVAAPI {
		args = append(args, "-vaapi_device", "/dev/dri/renderD128")
	}
	args = append(args, "-f", "flv", "-i", "pipe:0")

	gop := strconv.Itoa(h.SegmentDuration * 30)
	for _, r := range rungs {
		args = append(args,
			"-map", "0:v:0",
			"-map", "0:a:0",
			"-c:v", c.FFmpegEncoder(),
		)
		args = append(args, codecQualityArgs(c)...)
		args = append(args,
			"-s", fmt.Sprintf("%dx%d", r.Width, r.Height),
			"-b:v", fmt.Sprintf("%dk", r.BitrateKbps),
			"-maxrate", fmt.Sprintf("%dk", r.BitrateKbps),
			"-bufsize", fmt.Sprintf("%dk", r.BitrateKbps*2),
			"-profile:v", r.H264Profile,
			"-g", gop,
			"-keyint_min", gop,
			"-sc_threshold", "0",
			"-c:a", "aac",
			"-b:a", "128k",
		)
		args = append(args, hlsMuxerArgs(h, r.Name)...)
	}
	return args
}

// codecQualityArgs returns the per-encoder quality knobs.
func codecQualityArgs(c Codec) []string {
	switch c {
	case CodecNVENC:
		return []string{"-preset", "p3", "-tune", "ll", "-rc", "cbr"}
	case CodecQSV:
		return []string{"-preset", "veryfast", "-look_ahead", "0"}
	case CodecVAAPI:
		return []string{"-rc_mode", "CBR"}
	case CodecLibx264:
		return []string{"-preset", "veryfast", "-tune", "zerolatency"}
	}
	return nil
}

// hlsMuxerArgs assembles the per-rung muxer arguments. rungName is
// the rung subdirectory ("240p" / "1080p" / etc.); empty for
// passthrough's single-rung output.
func hlsMuxerArgs(h HLSOptions, rungName string) []string {
	scratch := h.ScratchDir
	if rungName != "" {
		scratch = filepath.Join(scratch, rungName)
	}
	if h.Legacy {
		return []string{
			"-f", "hls",
			"-hls_time", strconv.Itoa(h.SegmentDuration),
			"-hls_list_size", strconv.Itoa(h.PlaylistWindow),
			"-hls_segment_type", "mpegts",
			"-hls_flags", "delete_segments+append_list+omit_endlist+independent_segments",
			"-hls_segment_filename", filepath.Join(scratch, "segment_%05d.ts"),
			filepath.Join(scratch, "playlist.m3u8"),
		}
	}
	args := []string{
		"-f", "hls",
		"-hls_time", strconv.Itoa(h.SegmentDuration),
		"-hls_list_size", strconv.Itoa(h.PlaylistWindow),
		"-hls_segment_type", "fmp4",
		"-hls_flags", "delete_segments+append_list+omit_endlist+independent_segments+iframe_only_partial",
		"-hls_segment_filename", filepath.Join(scratch, "segment_%05d.m4s"),
		"-hls_fmp4_init_filename", "init.mp4",
	}
	if h.PartDuration > 0 {
		args = append(args, "-hls_part_duration", fmt.Sprintf("%g", h.PartDuration))
	}
	args = append(args, filepath.Join(scratch, "playlist.m3u8"))
	return args
}

// withHLSDefaults fills in zero-valued HLSOptions per LL-HLS / legacy
// canonical defaults.
func withHLSDefaults(h HLSOptions) HLSOptions {
	if h.SegmentDuration <= 0 {
		if h.Legacy {
			h.SegmentDuration = 6
		} else {
			h.SegmentDuration = 2
		}
	}
	if h.PlaylistWindow <= 0 {
		if h.Legacy {
			h.PlaylistWindow = 5
		} else {
			h.PlaylistWindow = 4
		}
	}
	if !h.Legacy && h.PartDuration <= 0 {
		h.PartDuration = 0.333
	}
	return h
}
