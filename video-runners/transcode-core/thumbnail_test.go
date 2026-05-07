package transcode

import (
	"strings"
	"testing"
)

func TestThumbnailCmd_Basic(t *testing.T) {
	cmd := ThumbnailCmd("/tmp/input.mp4", "/tmp/thumb.jpg", 10.5, 0, 0, HWProfile{})
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "ffmpeg") {
		t.Error("expected ffmpeg command")
	}
	if !strings.Contains(args, "-ss 10.500") {
		t.Errorf("expected -ss 10.500, got: %s", args)
	}
	if !strings.Contains(args, "-vframes 1") {
		t.Errorf("expected -vframes 1, got: %s", args)
	}
	if !strings.Contains(args, "-q:v 2") {
		t.Errorf("expected -q:v 2, got: %s", args)
	}
	if !strings.Contains(args, "/tmp/input.mp4") {
		t.Error("expected input path")
	}
	if !strings.Contains(args, "/tmp/thumb.jpg") {
		t.Error("expected output path")
	}
	// No scale filter when dimensions are 0
	if strings.Contains(args, "scale=") {
		t.Error("should not have scale filter when dimensions are 0")
	}
}

func TestThumbnailCmd_WithScale(t *testing.T) {
	cmd := ThumbnailCmd("/tmp/input.mp4", "/tmp/thumb.jpg", 5.0, 640, 360, HWProfile{})
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "scale=640:360") {
		t.Errorf("expected scale=640:360, got: %s", args)
	}
}

func TestThumbnailCmd_WidthOnly(t *testing.T) {
	cmd := ThumbnailCmd("/tmp/input.mp4", "/tmp/thumb.jpg", 5.0, 640, 0, HWProfile{})
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "scale=640:-2") {
		t.Errorf("expected scale=640:-2, got: %s", args)
	}
}

func TestThumbnailCmd_NoSeek(t *testing.T) {
	cmd := ThumbnailCmd("/tmp/input.mp4", "/tmp/thumb.jpg", 0, 0, 0, HWProfile{})
	args := strings.Join(cmd.Args, " ")

	if strings.Contains(args, "-ss") {
		t.Error("should not have -ss when seek is 0")
	}
}

func TestResolveSeekTime(t *testing.T) {
	tests := []struct {
		name     string
		seek     float64
		duration float64
		want     float64
	}{
		{"explicit seek", 10.0, 120.0, 10.0},
		{"default to 10%", 0, 120.0, 12.0},
		{"negative default", -1, 120.0, 12.0},
		{"seek beyond duration", 150.0, 120.0, 108.0}, // 90% of duration
		{"no duration", 0, 0, 0},
		{"explicit seek no duration", 5.0, 0, 5.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveSeekTime(tt.seek, tt.duration)
			diff := got - tt.want
			if diff < -0.01 || diff > 0.01 {
				t.Errorf("ResolveSeekTime(%f, %f) = %f, want %f", tt.seek, tt.duration, got, tt.want)
			}
		})
	}
}
