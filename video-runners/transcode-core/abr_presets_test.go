package transcode

import (
	"testing"
)

const testABRPresetsYAML = `
presets:
  - name: abr-standard
    description: "Standard 4-rung HLS ABR ladder"
    type: abr
    format: hls
    hls_mode: fmp4_single_file
    segment_duration: 6
    renditions:
      - name: "1080p"
        video:
          codec: h264
          width: 1920
          height: 1080
          bitrate: "5M"
          max_bitrate: "7.5M"
          profile: high
          level: "4.1"
        audio:
          codec: aac
          bitrate: "128k"
          channels: 2
      - name: "720p"
        video:
          codec: h264
          width: 1280
          height: 720
          bitrate: "2.5M"
          max_bitrate: "3.75M"
          profile: high
          level: "3.1"
        audio:
          codec: aac
          bitrate: "96k"
          channels: 2
      - name: "480p"
        video:
          codec: h264
          width: 854
          height: 480
          bitrate: "1M"
          max_bitrate: "1.5M"
          profile: main
          level: "3.0"
        audio:
          codec: aac
          bitrate: "96k"
          channels: 2
      - name: "360p"
        video:
          codec: h264
          width: 640
          height: 360
          bitrate: "600k"
          max_bitrate: "900k"
          profile: main
          level: "3.0"
        audio:
          codec: aac
          bitrate: "64k"
          channels: 2

  - name: abr-av1
    description: "AV1 top rungs with H.264 fallback"
    type: abr
    format: hls
    hls_mode: fmp4_single_file
    segment_duration: 6
    renditions:
      - name: "1080p-av1"
        video:
          codec: av1
          width: 1920
          height: 1080
          bitrate: "2.5M"
          max_bitrate: "3.75M"
        audio:
          codec: aac
          bitrate: "128k"
          channels: 2
      - name: "480p"
        video:
          codec: h264
          width: 854
          height: 480
          bitrate: "1M"
          max_bitrate: "1.5M"
          profile: main
          level: "3.0"
        audio:
          codec: aac
          bitrate: "96k"
          channels: 2

  - name: abr-with-audio
    description: "Preset with audio-only rendition"
    type: abr
    format: hls
    hls_mode: fmp4_single_file
    segment_duration: 6
    renditions:
      - name: "720p"
        video:
          codec: h264
          width: 1280
          height: 720
          bitrate: "2.5M"
          max_bitrate: "3.75M"
          profile: high
          level: "3.1"
        audio:
          codec: aac
          bitrate: "96k"
          channels: 2
      - name: "audio-only"
        audio:
          codec: aac
          bitrate: "64k"
          channels: 2
`

func TestLoadABRPresetsFromBytes(t *testing.T) {
	presets, err := LoadABRPresetsFromBytes([]byte(testABRPresetsYAML))
	if err != nil {
		t.Fatalf("LoadABRPresetsFromBytes() error: %v", err)
	}
	if len(presets) != 3 {
		t.Fatalf("expected 3 presets, got %d", len(presets))
	}

	p := presets[0]
	if p.Name != "abr-standard" {
		t.Errorf("name = %q, want abr-standard", p.Name)
	}
	if p.SegmentDuration != 6 {
		t.Errorf("segment_duration = %d, want 6", p.SegmentDuration)
	}
	if len(p.Renditions) != 4 {
		t.Fatalf("expected 4 renditions, got %d", len(p.Renditions))
	}
	if p.Renditions[0].Name != "1080p" {
		t.Errorf("first rendition name = %q, want 1080p", p.Renditions[0].Name)
	}
	if p.Renditions[0].Video.Width != 1920 {
		t.Errorf("first rendition width = %d, want 1920", p.Renditions[0].Video.Width)
	}
	if p.Renditions[0].Video.Bitrate != "5M" {
		t.Errorf("first rendition bitrate = %q, want 5M", p.Renditions[0].Video.Bitrate)
	}
}

func TestLoadABRPresetsFromBytes_DefaultSegmentDuration(t *testing.T) {
	yaml := `
presets:
  - name: test
    type: abr
    renditions:
      - name: "720p"
        video: { codec: h264, width: 1280, height: 720, bitrate: "2.5M" }
        audio: { codec: aac, bitrate: "96k", channels: 2 }
`
	presets, err := LoadABRPresetsFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if presets[0].SegmentDuration != 6 {
		t.Errorf("default segment_duration = %d, want 6", presets[0].SegmentDuration)
	}
}

func TestLoadABRPresetsFromBytes_Invalid(t *testing.T) {
	_, err := LoadABRPresetsFromBytes([]byte("not valid yaml: [[["))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}

	_, err = LoadABRPresetsFromBytes([]byte("presets: []"))
	if err == nil {
		t.Error("expected error for empty presets")
	}

	_, err = LoadABRPresetsFromBytes([]byte(`
presets:
  - name: empty
    type: abr
    renditions: []
`))
	if err == nil {
		t.Error("expected error for preset with no renditions")
	}
}

func TestValidateABRPresets(t *testing.T) {
	presets, _ := LoadABRPresetsFromBytes([]byte(testABRPresetsYAML))

	t.Run("with full GPU", func(t *testing.T) {
		hw := HWProfile{
			GPUName:  "RTX 4090",
			Encoders: []string{"h264_nvenc", "hevc_nvenc", "av1_nvenc"},
		}
		valid, skipped := ValidateABRPresets(presets, hw)
		if len(valid) != 3 {
			t.Errorf("expected 3 valid, got %d (skipped: %v)", len(valid), skipped)
		}
	})

	t.Run("without GPU", func(t *testing.T) {
		hw := HWProfile{}
		valid, skipped := ValidateABRPresets(presets, hw)
		if len(valid) != 0 {
			t.Errorf("expected 0 valid without GPU, got %d", len(valid))
		}
		if len(skipped) != 3 {
			t.Errorf("expected 3 skipped, got %d", len(skipped))
		}
	})

	t.Run("without AV1 encoder", func(t *testing.T) {
		hw := HWProfile{
			GPUName:  "RTX 2080",
			Encoders: []string{"h264_nvenc", "hevc_nvenc"},
		}
		valid, skipped := ValidateABRPresets(presets, hw)
		// abr-standard passes (all h264), abr-av1 fails (has av1), abr-with-audio passes (h264)
		if len(valid) != 2 {
			t.Errorf("expected 2 valid, got %d (skipped: %v)", len(valid), skipped)
		}
		if len(skipped) != 1 || skipped[0] != "abr-av1" {
			t.Errorf("expected skipped=[abr-av1], got %v", skipped)
		}
	})
}

func TestFindABRPreset(t *testing.T) {
	presets, _ := LoadABRPresetsFromBytes([]byte(testABRPresetsYAML))

	p, ok := FindABRPreset(presets, "abr-standard")
	if !ok {
		t.Fatal("expected to find abr-standard")
	}
	if p.Name != "abr-standard" {
		t.Errorf("got name %q", p.Name)
	}

	// Case insensitive
	_, ok = FindABRPreset(presets, "ABR-STANDARD")
	if !ok {
		t.Fatal("expected case-insensitive match")
	}

	// Not found
	_, ok = FindABRPreset(presets, "nonexistent")
	if ok {
		t.Error("expected not found")
	}
}

func TestABRPresetRenditionNames(t *testing.T) {
	presets, _ := LoadABRPresetsFromBytes([]byte(testABRPresetsYAML))
	names := presets[0].RenditionNames()
	expected := []string{"1080p", "720p", "480p", "360p"}
	if len(names) != len(expected) {
		t.Fatalf("expected %d names, got %d", len(expected), len(names))
	}
	for i, n := range names {
		if n != expected[i] {
			t.Errorf("name[%d] = %q, want %q", i, n, expected[i])
		}
	}
}

func TestABRPresetVideoRenditions(t *testing.T) {
	presets, _ := LoadABRPresetsFromBytes([]byte(testABRPresetsYAML))

	// abr-with-audio has 1 video + 1 audio-only
	p := presets[2]
	video := p.VideoRenditions()
	if len(video) != 1 {
		t.Errorf("expected 1 video rendition, got %d", len(video))
	}
	if video[0].Name != "720p" {
		t.Errorf("video rendition name = %q, want 720p", video[0].Name)
	}

	// All renditions should be returned by RenditionNames
	all := p.RenditionNames()
	if len(all) != 2 {
		t.Errorf("expected 2 total renditions, got %d", len(all))
	}
}
