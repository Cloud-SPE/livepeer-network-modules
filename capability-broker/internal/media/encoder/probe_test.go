package encoder

import (
	"strings"
	"testing"
)

func TestParseEncoders(t *testing.T) {
	output := `Encoders:
 V..... = Video
 ------
 V....D libx264              libx264 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10
 V....D h264_nvenc           NVIDIA NVENC H.264 encoder (codec h264)
 V....D h264_qsv             H.264 (Intel Quick Sync Video) (codec h264)
 V....D h264_vaapi           H.264/AVC (VAAPI) (codec h264)
`
	got := parseEncoders(output)
	want := []Codec{CodecLibx264, CodecNVENC, CodecQSV, CodecVAAPI}
	if len(got) != len(want) {
		t.Fatalf("len=%d want=%d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] got=%s want=%s", i, got[i], want[i])
		}
	}
}

func TestParseEncoders_OnlyCPU(t *testing.T) {
	got := parseEncoders(" V....D libx264              libx264 H.264 / AVC\n")
	if len(got) != 1 || got[0] != CodecLibx264 {
		t.Fatalf("got=%v want=[libx264]", got)
	}
}

func TestParseEncoders_None(t *testing.T) {
	got := parseEncoders("Encoders:\n V..... = Video\n")
	if len(got) != 0 {
		t.Fatalf("got=%v want=[]", got)
	}
}

func TestParseCodec(t *testing.T) {
	cases := []struct {
		in      string
		want    Codec
		wantErr bool
	}{
		{"auto", CodecAuto, false},
		{"NVENC", CodecNVENC, false},
		{"qsv", CodecQSV, false},
		{"vaapi", CodecVAAPI, false},
		{"libx264", CodecLibx264, false},
		{"unknown", "", true},
	}
	for _, c := range cases {
		got, err := ParseCodec(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseCodec(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("ParseCodec(%q)=%s want=%s", c.in, got, c.want)
		}
	}
}

func TestProbe_RefusesNoGPU(t *testing.T) {
	probeListEncodersForTest = func(string) []Codec { return []Codec{CodecLibx264} }
	t.Cleanup(func() { probeListEncodersForTest = nil })

	_, err := probeWithStub(ProbeOptions{Want: CodecAuto, AllowCPU: false})
	if err == nil {
		t.Fatalf("Probe with no GPU + AllowCPU=false: want error")
	}
	if !strings.Contains(err.Error(), "--encoder-allow-cpu=true") {
		t.Errorf("error message missing remediation: %v", err)
	}
}

func TestProbe_AllowsCPUOnOptIn(t *testing.T) {
	probeListEncodersForTest = func(string) []Codec { return []Codec{CodecLibx264} }
	t.Cleanup(func() { probeListEncodersForTest = nil })

	res, err := probeWithStub(ProbeOptions{Want: CodecAuto, AllowCPU: true})
	if err != nil {
		t.Fatalf("Probe with AllowCPU=true: %v", err)
	}
	if res.Selected != CodecLibx264 {
		t.Errorf("Selected=%s want=libx264", res.Selected)
	}
}

func TestProbe_GPUWinsOverCPU(t *testing.T) {
	probeListEncodersForTest = func(string) []Codec { return []Codec{CodecLibx264, CodecVAAPI} }
	t.Cleanup(func() { probeListEncodersForTest = nil })

	res, err := probeWithStub(ProbeOptions{Want: CodecAuto, AllowCPU: true})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if res.Selected != CodecVAAPI {
		t.Errorf("Selected=%s want=vaapi (GPU wins over CPU)", res.Selected)
	}
}

func TestProbe_PreferenceOrder(t *testing.T) {
	probeListEncodersForTest = func(string) []Codec {
		return []Codec{CodecLibx264, CodecVAAPI, CodecQSV, CodecNVENC}
	}
	t.Cleanup(func() { probeListEncodersForTest = nil })

	res, err := probeWithStub(ProbeOptions{Want: CodecAuto, AllowCPU: true})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if res.Selected != CodecNVENC {
		t.Errorf("Selected=%s want=nvenc (preference order NVENC→QSV→VAAPI→libx264)", res.Selected)
	}
}

func TestProbe_ExplicitMissing(t *testing.T) {
	probeListEncodersForTest = func(string) []Codec { return []Codec{CodecLibx264} }
	t.Cleanup(func() { probeListEncodersForTest = nil })

	_, err := probeWithStub(ProbeOptions{Want: CodecNVENC})
	if err == nil {
		t.Fatalf("Probe explicit nvenc with libx264-only: want error")
	}
}
