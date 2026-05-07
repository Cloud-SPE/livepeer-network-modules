package transcode

import (
	"testing"
	"time"
)

func TestParseProgressLine(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantMatch bool
		wantFrame int
		wantFPS   float64
		wantSpeed float64
	}{
		{
			name:      "typical line",
			line:      "frame=  150 fps= 45.2 q=28.0 size=    1024kB time=00:00:05.00 bitrate=1677.7kbits/s speed=1.51x",
			wantMatch: true,
			wantFrame: 150,
			wantFPS:   45.2,
			wantSpeed: 1.51,
		},
		{
			name:      "large values",
			line:      "frame= 8500 fps=120.5 q=26.0 Lsize=  256000kB time=00:04:43.33 bitrate=7394.5kbits/s speed=4.02x",
			wantMatch: true,
			wantFrame: 8500,
			wantFPS:   120.5,
			wantSpeed: 4.02,
		},
		{
			name:      "start of encoding",
			line:      "frame=    1 fps=0.0 q=0.0 size=       0kB time=00:00:00.04 bitrate=   0.0kbits/s speed=0.08x",
			wantMatch: true,
			wantFrame: 1,
			wantFPS:   0.0,
			wantSpeed: 0.08,
		},
		{
			name:      "not a progress line",
			line:      "Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'input.mp4':",
			wantMatch: false,
		},
		{
			name:      "empty line",
			line:      "",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, ok := ParseProgressLine(tt.line)
			if ok != tt.wantMatch {
				t.Fatalf("ParseProgressLine() match = %v, want %v", ok, tt.wantMatch)
			}
			if !ok {
				return
			}
			if info.Frame != tt.wantFrame {
				t.Errorf("Frame = %d, want %d", info.Frame, tt.wantFrame)
			}
			if info.FPS != tt.wantFPS {
				t.Errorf("FPS = %f, want %f", info.FPS, tt.wantFPS)
			}
			if info.Speed != tt.wantSpeed {
				t.Errorf("Speed = %f, want %f", info.Speed, tt.wantSpeed)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"zero", "00:00:00.00", 0, false},
		{"5 seconds", "00:00:05.00", 5 * time.Second, false},
		{"1 minute 30s", "00:01:30.00", 90 * time.Second, false},
		{"1 hour", "01:00:00.00", time.Hour, false},
		{"fractional", "00:00:10.50", 10*time.Second + 500*time.Millisecond, false},
		{"real ffmpeg", "00:04:43.33", 4*time.Minute + 43*time.Second + 330*time.Millisecond, false},
		{"N/A", "N/A", 0, true},
		{"empty", "", 0, true},
		{"invalid format", "10.5", 0, true},
		{"invalid hours", "xx:00:00.00", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseDuration(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got != tt.expected {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestCalcPercent(t *testing.T) {
	tests := []struct {
		name     string
		current  time.Duration
		total    time.Duration
		expected float64
	}{
		{"zero total", time.Second, 0, 0},
		{"50%", 5 * time.Second, 10 * time.Second, 50.0},
		{"100%", 10 * time.Second, 10 * time.Second, 100.0},
		{"over 100%", 12 * time.Second, 10 * time.Second, 100.0},
		{"start", 0, 10 * time.Second, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalcPercent(tt.current, tt.total)
			if got != tt.expected {
				t.Errorf("CalcPercent() = %f, want %f", got, tt.expected)
			}
		})
	}
}
