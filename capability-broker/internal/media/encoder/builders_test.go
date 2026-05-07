package encoder

import "testing"

func argsContain(args []string, want ...string) bool {
	for i := 0; i+len(want) <= len(args); i++ {
		match := true
		for j, w := range want {
			if args[i+j] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestNVENCQualityArgs(t *testing.T) {
	got := nvencQualityArgs()
	for _, want := range [][]string{
		{"-preset", "p3"},
		{"-tune", "ll"},
		{"-rc", "cbr"},
	} {
		if !argsContain(got, want...) {
			t.Errorf("missing %v in %v", want, got)
		}
	}
}

func TestQSVQualityArgs(t *testing.T) {
	got := qsvQualityArgs()
	if !argsContain(got, "-look_ahead", "0") {
		t.Errorf("qsv missing -look_ahead 0: %v", got)
	}
}

func TestVAAPIQualityArgs(t *testing.T) {
	got := vaapiQualityArgs()
	if !argsContain(got, "-rc_mode", "CBR") {
		t.Errorf("vaapi missing -rc_mode CBR: %v", got)
	}
}

func TestLibx264QualityArgs(t *testing.T) {
	got := libx264QualityArgs()
	if !argsContain(got, "-tune", "zerolatency") {
		t.Errorf("libx264 missing zerolatency: %v", got)
	}
}

func TestHLSMuxerArgs_LLHLS_PartDuration(t *testing.T) {
	args := hlsMuxerArgs(withHLSDefaults(HLSOptions{ScratchDir: "/x"}), "")
	if !argsContain(args, "-hls_part_duration", "0.333") {
		t.Errorf("LL-HLS missing default part duration: %v", args)
	}
	if !argsContain(args, "-hls_segment_type", "fmp4") {
		t.Errorf("LL-HLS missing fmp4: %v", args)
	}
}

func TestCodecPerRungScaleArgs_VAAPI(t *testing.T) {
	r := Rung{Name: "720p", Width: 1280, Height: 720}
	got := codecPerRungScaleArgs(CodecVAAPI, r)
	if !argsContain(got, "-vf", "format=nv12,hwupload,scale_vaapi=w=1280:h=720") {
		t.Errorf("vaapi scale chain missing: %v", got)
	}
}

func TestCodecPerRungScaleArgs_QSV(t *testing.T) {
	r := Rung{Name: "720p", Width: 1280, Height: 720}
	got := codecPerRungScaleArgs(CodecQSV, r)
	if !argsContain(got, "-vf", "scale_qsv=w=1280:h=720") {
		t.Errorf("qsv scale chain missing: %v", got)
	}
}

func TestCodecPerRungScaleArgs_NVENC(t *testing.T) {
	r := Rung{Name: "720p", Width: 1280, Height: 720}
	got := codecPerRungScaleArgs(CodecNVENC, r)
	if !argsContain(got, "-s", "1280x720") {
		t.Errorf("nvenc -s shape missing: %v", got)
	}
}
