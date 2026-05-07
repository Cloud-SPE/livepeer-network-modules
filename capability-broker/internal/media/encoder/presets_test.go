package encoder

import (
	"strings"
	"testing"
)

func TestBuildArgs_Passthrough(t *testing.T) {
	args, err := BuildArgs(PresetInput{
		Profile: ProfilePassthrough,
		HLS:     HLSOptions{ScratchDir: "/scratch/sess_a"},
	})
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c:v copy") {
		t.Errorf("passthrough missing -c:v copy: %s", joined)
	}
	if !strings.Contains(joined, "-hls_segment_type fmp4") {
		t.Errorf("passthrough missing fmp4 segments: %s", joined)
	}
	if !strings.Contains(joined, "-progress pipe:2") {
		t.Errorf("passthrough missing progress pipe: %s", joined)
	}
	if !strings.Contains(joined, "/scratch/sess_a/playlist.m3u8") {
		t.Errorf("passthrough missing playlist path: %s", joined)
	}
}

func TestBuildArgs_PassthroughLegacy(t *testing.T) {
	args, err := BuildArgs(PresetInput{
		Profile: ProfilePassthrough,
		HLS:     HLSOptions{ScratchDir: "/scratch", Legacy: true},
	})
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-hls_segment_type mpegts") {
		t.Errorf("legacy missing mpegts: %s", joined)
	}
	if strings.Contains(joined, "iframe_only_partial") {
		t.Errorf("legacy should not have iframe_only_partial: %s", joined)
	}
	if !strings.Contains(joined, "-hls_time 6") {
		t.Errorf("legacy default segment duration: %s", joined)
	}
}

func TestBuildArgs_Libx264FiveRungs(t *testing.T) {
	args, err := BuildArgs(PresetInput{
		Profile: ProfileLibx264_1080p,
		Codec:   CodecLibx264,
		HLS:     HLSOptions{ScratchDir: "/scratch/sess_b"},
	})
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, rung := range FiveRungLadder {
		if !strings.Contains(joined, "/scratch/sess_b/"+rung.Name+"/playlist.m3u8") {
			t.Errorf("missing %s rung playlist: %s", rung.Name, joined)
		}
	}
	if !strings.Contains(joined, "-c:v libx264") {
		t.Errorf("libx264 codec missing: %s", joined)
	}
	if !strings.Contains(joined, "-tune zerolatency") {
		t.Errorf("libx264 quality args missing: %s", joined)
	}
	if !strings.Contains(joined, "-profile:v baseline") {
		t.Errorf("baseline profile (240p/360p) missing: %s", joined)
	}
	if !strings.Contains(joined, "-profile:v high") {
		t.Errorf("high profile (1080p) missing: %s", joined)
	}
}

func TestBuildArgs_NVENC(t *testing.T) {
	args, _ := BuildArgs(PresetInput{
		Profile: ProfileNVENC_1080p,
		HLS:     HLSOptions{ScratchDir: "/scratch"},
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c:v h264_nvenc") {
		t.Errorf("NVENC encoder name missing: %s", joined)
	}
	if !strings.Contains(joined, "-tune ll") {
		t.Errorf("NVENC ll tuning missing: %s", joined)
	}
}

func TestBuildArgs_VAAPI_DeviceFlag(t *testing.T) {
	args, _ := BuildArgs(PresetInput{
		Profile: ProfileVAAPI_1080p,
		HLS:     HLSOptions{ScratchDir: "/scratch"},
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-vaapi_device /dev/dri/renderD128") {
		t.Errorf("vaapi device flag missing: %s", joined)
	}
	if !strings.Contains(joined, "-c:v h264_vaapi") {
		t.Errorf("vaapi encoder name missing: %s", joined)
	}
}

func TestBuildArgs_RequiresScratch(t *testing.T) {
	_, err := BuildArgs(PresetInput{Profile: ProfilePassthrough})
	if err == nil {
		t.Fatalf("BuildArgs without ScratchDir: want error")
	}
}

func TestBuildArgs_UnknownProfile(t *testing.T) {
	_, err := BuildArgs(PresetInput{
		Profile: "nope",
		HLS:     HLSOptions{ScratchDir: "/x"},
	})
	if err == nil {
		t.Fatalf("BuildArgs with unknown profile: want error")
	}
}

func TestMatchesCodec(t *testing.T) {
	cases := []struct {
		profile string
		codec   Codec
		want    bool
	}{
		{ProfilePassthrough, CodecLibx264, true},
		{ProfilePassthrough, CodecNVENC, true},
		{ProfileLibx264_1080p, CodecLibx264, true},
		{ProfileLibx264_1080p, CodecNVENC, true},
		{ProfileNVENC_1080p, CodecNVENC, true},
		{ProfileNVENC_1080p, CodecLibx264, false},
		{ProfileQSV_1080p, CodecQSV, true},
		{ProfileQSV_1080p, CodecVAAPI, false},
		{ProfileVAAPI_1080p, CodecVAAPI, true},
	}
	for _, c := range cases {
		if got := MatchesCodec(c.profile, c.codec); got != c.want {
			t.Errorf("MatchesCodec(%q, %s)=%v want=%v", c.profile, c.codec, got, c.want)
		}
	}
}
