package transcode

import (
	"testing"
)

func TestMaxSessionsForGPU(t *testing.T) {
	tests := []struct {
		name     string
		gpuName  string
		vendor   GPUVendor
		expected int
	}{
		// NVIDIA
		{"GeForce RTX 4090", "NVIDIA GeForce RTX 4090", VendorNVIDIA, 5},
		{"GeForce RTX 5090", "NVIDIA GeForce RTX 5090", VendorNVIDIA, 5},
		{"GTX 1080 Ti", "NVIDIA GeForce GTX 1080 Ti", VendorNVIDIA, 5},
		{"Tesla V100", "Tesla V100-SXM2-16GB", VendorNVIDIA, 0},
		{"A100", "NVIDIA A100-SXM4-80GB", VendorNVIDIA, 0},
		{"A10", "NVIDIA A10", VendorNVIDIA, 0},
		{"L40", "NVIDIA L40", VendorNVIDIA, 0},
		{"L4", "NVIDIA L4", VendorNVIDIA, 0},
		{"H100", "NVIDIA H100 80GB HBM3", VendorNVIDIA, 0},
		{"Quadro RTX 8000", "Quadro RTX 8000", VendorNVIDIA, 0},
		{"Unknown NVIDIA GPU", "Some Unknown GPU", VendorNVIDIA, 5},
		{"Empty NVIDIA name", "", VendorNVIDIA, 5},
		// Intel
		{"Intel Arc A770", "Intel(R) Arc A770", VendorIntel, 8},
		{"Intel Xe Graphics", "Intel(R) Xe Graphics", VendorIntel, 8},
		// AMD
		{"AMD RX 7900 XTX", "AMD Radeon RX 7900 XTX", VendorAMD, 8},
		{"AMD RX 6800", "AMD Radeon RX 6800", VendorAMD, 8},
		// No vendor
		{"No vendor", "", VendorNone, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maxSessionsForGPU(tt.gpuName, tt.vendor)
			if result != tt.expected {
				t.Errorf("maxSessionsForGPU(%q, %q) = %d, want %d", tt.gpuName, tt.vendor, result, tt.expected)
			}
		})
	}
}

func TestHasEncoder(t *testing.T) {
	hw := HWProfile{
		Encoders: []string{"h264_nvenc", "hevc_nvenc", "av1_nvenc"},
	}

	tests := []struct {
		name     string
		encoder  string
		expected bool
	}{
		{"has h264_nvenc", "h264_nvenc", true},
		{"has hevc_nvenc", "hevc_nvenc", true},
		{"has av1_nvenc", "av1_nvenc", true},
		{"case insensitive", "H264_NVENC", true},
		{"not present", "libx264", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hw.HasEncoder(tt.encoder); got != tt.expected {
				t.Errorf("HasEncoder(%q) = %v, want %v", tt.encoder, got, tt.expected)
			}
		})
	}
}

func TestHasDecoder(t *testing.T) {
	hw := HWProfile{
		Decoders: []string{"h264_cuvid", "hevc_cuvid"},
	}

	if !hw.HasDecoder("h264_cuvid") {
		t.Error("expected HasDecoder(h264_cuvid) = true")
	}
	if hw.HasDecoder("av1_cuvid") {
		t.Error("expected HasDecoder(av1_cuvid) = false")
	}
}

func TestHasHWAccel(t *testing.T) {
	hw := HWProfile{
		HWAccels: []string{"cuda", "vaapi"},
	}

	if !hw.HasHWAccel("cuda") {
		t.Error("expected HasHWAccel(cuda) = true")
	}
	if !hw.HasHWAccel("CUDA") {
		t.Error("expected HasHWAccel(CUDA) = true (case insensitive)")
	}
	if hw.HasHWAccel("qsv") {
		t.Error("expected HasHWAccel(qsv) = false")
	}
}

func TestIsGPUAvailable(t *testing.T) {
	tests := []struct {
		name     string
		hw       HWProfile
		expected bool
	}{
		{"NVIDIA GPU with encoders", HWProfile{GPUName: "RTX 4090", Vendor: VendorNVIDIA, Encoders: []string{"h264_nvenc"}}, true},
		{"Intel GPU with encoders", HWProfile{GPUName: "Intel Arc A770", Vendor: VendorIntel, Encoders: []string{"h264_qsv"}}, true},
		{"AMD GPU with encoders", HWProfile{GPUName: "RX 7900 XTX", Vendor: VendorAMD, Encoders: []string{"h264_vaapi"}}, true},
		{"No GPU name", HWProfile{GPUName: "", Encoders: []string{"h264_nvenc"}}, false},
		{"No encoders", HWProfile{GPUName: "RTX 4090", Encoders: nil}, false},
		{"Empty profile", HWProfile{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.hw.IsGPUAvailable(); got != tt.expected {
				t.Errorf("IsGPUAvailable() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDetectVAAPIDevice(t *testing.T) {
	// detectVAAPIDevice should always return a non-empty string
	device := detectVAAPIDevice()
	if device == "" {
		t.Error("detectVAAPIDevice() returned empty string")
	}
	// Should default to /dev/dri/renderD128 if no DRI devices found
	if device != "/dev/dri/renderD128" && !isRenderNode(device) {
		t.Errorf("detectVAAPIDevice() = %q, expected /dev/dri/renderD128 or a renderD* path", device)
	}
}

func isRenderNode(path string) bool {
	return len(path) > 0 && path[:len("/dev/dri/renderD")] == "/dev/dri/renderD"
}

func TestParseVAInfoGPUName(t *testing.T) {
	tests := []struct {
		name     string
		vainfo   string
		expected string
	}{
		{
			"Intel iHD driver",
			"vainfo: Driver version: Intel iHD driver for Intel(R) Gen Graphics - 23.1.1 - Intel(R) Xe Graphics",
			"Intel(R) Xe Graphics",
		},
		{
			"AMD radeonsi driver",
			"vainfo: Driver version: Mesa Gallium driver 23.0.4 for AMD Radeon RX 7900 XTX - AMDGPU Navi31",
			"AMDGPU Navi31",
		},
		{
			"No dash in output",
			"vainfo: Driver version: some driver",
			"some driver",
		},
		{
			"Empty input",
			"",
			"Unknown GPU",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseVAInfoGPUName(tt.vainfo)
			if got != tt.expected {
				t.Errorf("parseVAInfoGPUName() = %q, want %q", got, tt.expected)
			}
		})
	}
}
