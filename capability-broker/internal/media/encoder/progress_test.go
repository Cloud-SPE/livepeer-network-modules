package encoder

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseProgressStream(t *testing.T) {
	in := strings.Join([]string{
		"frame=0",
		"out_time_us=0",
		"progress=continue",
		"frame=120",
		"out_time_us=4000000",
		"progress=continue",
		"frame=200",
		"out_time_us=6660000",
		"progress=end",
	}, "\n")

	var p Progress
	p.Width = 1920
	p.Height = 1080
	if err := parseProgressStream(strings.NewReader(in), &p, nil); err != nil {
		t.Fatalf("parseProgressStream: %v", err)
	}
	if got := p.CurrentFrames(); got != 200 {
		t.Errorf("CurrentFrames=%d want=200", got)
	}
	if got := p.CurrentUnits(); got != 7 {
		t.Errorf("CurrentUnits=%d want=7 (ceil(6.66))", got)
	}
	if got := p.CurrentFrameMegapixels(); got != 414 {
		t.Errorf("CurrentFrameMegapixels=%d want=414", got)
	}
}

func TestParseProgressStream_Capture(t *testing.T) {
	in := "frame=10\nout_time_us=1000000\nprogress=continue\n"
	var p Progress
	var capture bytes.Buffer
	if err := parseProgressStream(strings.NewReader(in), &p, &capture); err != nil {
		t.Fatalf("parseProgressStream: %v", err)
	}
	if !strings.Contains(capture.String(), "frame=10") {
		t.Errorf("capture missing frame line: %q", capture.String())
	}
}

func TestProgress_CurrentUnitsZero(t *testing.T) {
	var p Progress
	if got := p.CurrentUnits(); got != 0 {
		t.Errorf("zero progress: CurrentUnits=%d want=0", got)
	}
}

func TestProgress_MonotonicMaxOnly(t *testing.T) {
	in := "frame=10\nframe=5\nframe=20\nout_time_us=2000\nout_time_us=1000\nout_time_us=3000\n"
	var p Progress
	_ = parseProgressStream(strings.NewReader(in), &p, nil)
	if p.CurrentFrames() != 20 {
		t.Errorf("CurrentFrames=%d want=20 (monotonic max)", p.CurrentFrames())
	}
	if p.OutTimeUs.Load() != 3000 {
		t.Errorf("OutTimeUs=%d want=3000 (monotonic max)", p.OutTimeUs.Load())
	}
}
