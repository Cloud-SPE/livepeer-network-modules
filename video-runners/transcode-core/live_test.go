package transcode

import (
	"strings"
	"testing"
)

func TestLiveTranscodeCmd_NVIDIA(t *testing.T) {
	hw := HWProfile{
		GPUName:  "RTX 4090",
		Vendor:   VendorNVIDIA,
		Encoders: []string{"h264_nvenc", "hevc_nvenc"},
		HWAccels: []string{"cuda"},
	}
	params := LiveTranscodeParams{
		VideoCodec:   "h264",
		Width:        1920,
		Height:       1080,
		Bitrate:      "4M",
		MaxRate:      "6M",
		BufSize:      "12M",
		AudioCodec:   "aac",
		AudioBitrate: "128k",
	}

	cmd := LiveTranscodeCmd(params, hw)
	args := strings.Join(cmd.Args, " ")

	// Low-latency flags
	if !strings.Contains(args, "-fflags +nobuffer") {
		t.Error("expected -fflags +nobuffer")
	}
	if !strings.Contains(args, "-flags +low_delay") {
		t.Error("expected -flags +low_delay")
	}

	// NVIDIA hwaccel
	if !strings.Contains(args, "-hwaccel cuda") {
		t.Error("expected -hwaccel cuda")
	}
	if !strings.Contains(args, "-hwaccel_output_format cuda") {
		t.Error("expected -hwaccel_output_format cuda")
	}

	// MPEG-TS pipe I/O
	if !strings.Contains(args, "-f mpegts -i pipe:0") {
		t.Error("expected -f mpegts -i pipe:0")
	}
	if !strings.Contains(args, "-f mpegts pipe:1") {
		t.Error("expected -f mpegts pipe:1")
	}

	// NVENC encoder + tuning
	if !strings.Contains(args, "-c:v h264_nvenc") {
		t.Error("expected -c:v h264_nvenc")
	}
	if !strings.Contains(args, "-preset p4") {
		t.Error("expected NVENC preset p4")
	}
	if !strings.Contains(args, "-tune hq") {
		t.Error("expected NVENC tune hq")
	}

	// Bitrate
	if !strings.Contains(args, "-b:v 4M") {
		t.Error("expected -b:v 4M")
	}
	if !strings.Contains(args, "-maxrate 6M") {
		t.Error("expected -maxrate 6M")
	}
	if !strings.Contains(args, "-bufsize 12M") {
		t.Error("expected -bufsize 12M")
	}

	// Scale filter
	if !strings.Contains(args, "scale_cuda=1920:1080") {
		t.Errorf("expected scale_cuda=1920:1080, got: %s", args)
	}

	// Audio
	if !strings.Contains(args, "-c:a aac") {
		t.Error("expected -c:a aac")
	}
	if !strings.Contains(args, "-b:a 128k") {
		t.Error("expected -b:a 128k")
	}

	// No -movflags (streaming, not file)
	if strings.Contains(args, "-movflags") {
		t.Error("should not have -movflags for live streaming")
	}
}

func TestLiveTranscodeCmd_Intel(t *testing.T) {
	hw := HWProfile{
		GPUName:    "Intel(R) Arc A770",
		Vendor:     VendorIntel,
		DevicePath: "/dev/dri/renderD128",
		Encoders:   []string{"h264_qsv", "hevc_qsv"},
		HWAccels:   []string{"qsv", "vaapi"},
	}
	params := LiveTranscodeParams{
		VideoCodec: "h264",
		Width:      1280,
		Height:     720,
		Bitrate:    "2500k",
	}

	cmd := LiveTranscodeCmd(params, hw)
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "-hwaccel qsv") {
		t.Errorf("expected -hwaccel qsv, got: %s", args)
	}
	if !strings.Contains(args, "-c:v h264_qsv") {
		t.Errorf("expected -c:v h264_qsv, got: %s", args)
	}
	if !strings.Contains(args, "-preset medium") {
		t.Errorf("expected -preset medium for QSV, got: %s", args)
	}
	if !strings.Contains(args, "scale_qsv=w=1280:h=720") {
		t.Errorf("expected scale_qsv=w=1280:h=720, got: %s", args)
	}
}

func TestLiveTranscodeCmd_AMD(t *testing.T) {
	hw := HWProfile{
		GPUName:    "AMD Radeon RX 7900 XTX",
		Vendor:     VendorAMD,
		DevicePath: "/dev/dri/renderD128",
		Encoders:   []string{"h264_vaapi", "hevc_vaapi"},
		HWAccels:   []string{"vaapi"},
	}
	params := LiveTranscodeParams{
		VideoCodec: "h264",
		Width:      1920,
		Height:     1080,
		Bitrate:    "5M",
	}

	cmd := LiveTranscodeCmd(params, hw)
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "-hwaccel vaapi") {
		t.Errorf("expected -hwaccel vaapi, got: %s", args)
	}
	if !strings.Contains(args, "-hwaccel_device /dev/dri/renderD128") {
		t.Errorf("expected -hwaccel_device, got: %s", args)
	}
	if !strings.Contains(args, "-c:v h264_vaapi") {
		t.Errorf("expected -c:v h264_vaapi, got: %s", args)
	}
	if !strings.Contains(args, "format=nv12,hwupload,scale_vaapi=w=1920:h=1080") {
		t.Errorf("expected VAAPI hwupload + scale filter, got: %s", args)
	}
}

func TestLiveTranscodeCmd_NoScale(t *testing.T) {
	hw := HWProfile{
		GPUName:  "RTX 4090",
		Vendor:   VendorNVIDIA,
		Encoders: []string{"h264_nvenc"},
		HWAccels: []string{"cuda"},
	}
	params := LiveTranscodeParams{
		VideoCodec: "h264",
		Bitrate:    "4M",
	}

	cmd := LiveTranscodeCmd(params, hw)
	args := strings.Join(cmd.Args, " ")

	if strings.Contains(args, "scale") {
		t.Error("should not have scale filter when width and height are 0")
	}
	if strings.Contains(args, "-vf") {
		t.Error("should not have -vf when no scaling needed")
	}
}

func TestLiveTranscodeCmd_AudioCopy(t *testing.T) {
	hw := HWProfile{
		GPUName:  "RTX 4090",
		Vendor:   VendorNVIDIA,
		Encoders: []string{"h264_nvenc"},
		HWAccels: []string{"cuda"},
	}
	params := LiveTranscodeParams{
		VideoCodec:   "h264",
		Bitrate:      "4M",
		AudioCodec:   "copy",
		AudioBitrate: "128k",
	}

	cmd := LiveTranscodeCmd(params, hw)
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "-c:a copy") {
		t.Error("expected -c:a copy")
	}
	// Audio bitrate should not be set when codec is copy
	if strings.Contains(args, "-b:a") {
		t.Error("should not have -b:a when audio codec is copy")
	}
}

func TestLiveTranscodeCmd_FPS(t *testing.T) {
	hw := HWProfile{}
	params := LiveTranscodeParams{
		VideoCodec: "h264",
		Bitrate:    "2M",
		FPS:        30,
	}

	cmd := LiveTranscodeCmd(params, hw)
	args := strings.Join(cmd.Args, " ")

	if !strings.Contains(args, "-r 30") {
		t.Errorf("expected -r 30 for FPS override, got: %s", args)
	}
}

func TestLiveTranscodeCmd_DefaultAudio(t *testing.T) {
	hw := HWProfile{}
	params := LiveTranscodeParams{
		VideoCodec: "h264",
		Bitrate:    "2M",
	}

	cmd := LiveTranscodeCmd(params, hw)
	args := strings.Join(cmd.Args, " ")

	// Default audio codec should be aac
	if !strings.Contains(args, "-c:a aac") {
		t.Errorf("expected default -c:a aac, got: %s", args)
	}
}
