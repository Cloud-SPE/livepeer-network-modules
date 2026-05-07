package encoder

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Codec identifies an FFmpeg encoder family.
type Codec string

const (
	CodecAuto    Codec = "auto"
	CodecNVENC   Codec = "nvenc"
	CodecQSV     Codec = "qsv"
	CodecVAAPI   Codec = "vaapi"
	CodecLibx264 Codec = "libx264"
)

// ParseCodec validates the operator-supplied --encoder flag.
func ParseCodec(s string) (Codec, error) {
	switch Codec(strings.ToLower(s)) {
	case CodecAuto, CodecNVENC, CodecQSV, CodecVAAPI, CodecLibx264:
		return Codec(strings.ToLower(s)), nil
	}
	return "", fmt.Errorf("encoder: unknown codec %q (want auto|nvenc|qsv|vaapi|libx264)", s)
}

// FFmpegEncoder is the canonical libavcodec encoder name (e.g.
// `h264_nvenc`).
func (c Codec) FFmpegEncoder() string {
	switch c {
	case CodecNVENC:
		return "h264_nvenc"
	case CodecQSV:
		return "h264_qsv"
	case CodecVAAPI:
		return "h264_vaapi"
	case CodecLibx264:
		return "libx264"
	}
	return ""
}

// IsGPU reports whether the codec is hardware-accelerated.
func (c Codec) IsGPU() bool {
	switch c {
	case CodecNVENC, CodecQSV, CodecVAAPI:
		return true
	}
	return false
}

// ProbeOptions controls how Probe selects an encoder.
type ProbeOptions struct {
	// Want is the operator-supplied codec preference. "auto" walks
	// the preference order NVENC → QSV → VAAPI → libx264.
	Want Codec
	// AllowCPU permits libx264 fallback when no GPU encoder is
	// available. Default false: the broker refuses to start in
	// auto-mode on a hardware-less host.
	AllowCPU bool
	// Bin overrides the ffmpeg binary path.
	Bin string
	// Timeout caps how long the probe waits on each `ffmpeg
	// -encoders` call.
	Timeout time.Duration
}

// ProbeResult is the outcome of an encoder selection.
type ProbeResult struct {
	// Selected is the codec the broker will use.
	Selected Codec
	// Available lists every encoder the probe found in the local
	// FFmpeg build, in preference order.
	Available []Codec
}

// probeListEncodersForTest is a test seam. Production runs nil.
var probeListEncodersForTest func(bin string) []Codec

// probeWithStub is the test entry point: bypasses Probe's bin/timeout
// resolution and uses probeListEncodersForTest.
func probeWithStub(opts ProbeOptions) (ProbeResult, error) {
	want, err := ParseCodec(string(opts.Want))
	if err != nil {
		return ProbeResult{}, err
	}
	available := probeListEncodersForTest("ffmpeg")
	return selectEncoder(want, available, opts.AllowCPU)
}

// Probe selects an encoder. Returns an error in the no-GPU + no-CPU
// case so the broker fails fast at startup.
func Probe(opts ProbeOptions) (ProbeResult, error) {
	bin := opts.Bin
	if bin == "" {
		bin = "ffmpeg"
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	want, err := ParseCodec(string(opts.Want))
	if err != nil {
		return ProbeResult{}, err
	}

	var available []Codec
	if probeListEncodersForTest != nil {
		available = probeListEncodersForTest(bin)
	} else {
		available, err = listEncoders(bin, timeout)
		if err != nil {
			return ProbeResult{}, fmt.Errorf("encoder probe: %w", err)
		}
	}
	return selectEncoder(want, available, opts.AllowCPU)
}

// selectEncoder applies the preference rules to the available list.
// Pure function so probe_test can drive it.
func selectEncoder(want Codec, available []Codec, allowCPU bool) (ProbeResult, error) {

	if want != CodecAuto {
		for _, c := range available {
			if c == want {
				return ProbeResult{Selected: want, Available: available}, nil
			}
		}
		return ProbeResult{Available: available}, fmt.Errorf("encoder %q not available in this ffmpeg build (have: %v)", want, available)
	}

	for _, c := range []Codec{CodecNVENC, CodecQSV, CodecVAAPI} {
		for _, a := range available {
			if a == c {
				return ProbeResult{Selected: c, Available: available}, nil
			}
		}
	}

	for _, a := range available {
		if a == CodecLibx264 {
			if !allowCPU {
				return ProbeResult{Available: available}, errors.New(
					"no GPU encoder detected; install NVIDIA driver + cuda-toolkit, OR set " +
						"--encoder-allow-cpu=true to use libx264 (production deployments should use a GPU)")
			}
			return ProbeResult{Selected: CodecLibx264, Available: available}, nil
		}
	}
	return ProbeResult{Available: available}, errors.New("no usable H.264 encoder in this ffmpeg build (looked for h264_nvenc, h264_qsv, h264_vaapi, libx264)")
}

// listEncoders shells out to `ffmpeg -hide_banner -encoders` and
// returns the H.264 encoders it finds. The order in the returned
// slice matches the order the FFmpeg build reports.
func listEncoders(bin string, timeout time.Duration) ([]Codec, error) {
	cmd := exec.Command(bin, "-hide_banner", "-encoders")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	doneCh := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}
	go func() { doneCh <- cmd.Wait() }()
	select {
	case err := <-doneCh:
		if err != nil {
			return nil, fmt.Errorf("%s -encoders: %w", bin, err)
		}
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-doneCh
		return nil, fmt.Errorf("%s -encoders timed out after %s", bin, timeout)
	}
	return parseEncoders(out.String()), nil
}

// parseEncoders walks `ffmpeg -encoders` output line-by-line and
// returns the H.264 encoders it recognizes. Pure function for test.
func parseEncoders(s string) []Codec {
	var found []Codec
	seen := map[Codec]bool{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.Contains(line, " h264_nvenc "):
			if !seen[CodecNVENC] {
				found = append(found, CodecNVENC)
				seen[CodecNVENC] = true
			}
		case strings.Contains(line, " h264_qsv "):
			if !seen[CodecQSV] {
				found = append(found, CodecQSV)
				seen[CodecQSV] = true
			}
		case strings.Contains(line, " h264_vaapi "):
			if !seen[CodecVAAPI] {
				found = append(found, CodecVAAPI)
				seen[CodecVAAPI] = true
			}
		case strings.Contains(line, " libx264 "):
			if !seen[CodecLibx264] {
				found = append(found, CodecLibx264)
				seen[CodecLibx264] = true
			}
		}
	}
	return found
}
