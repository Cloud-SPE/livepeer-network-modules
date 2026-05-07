package transcode

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// GPUVendor identifies the GPU manufacturer.
type GPUVendor string

const (
	VendorNone   GPUVendor = ""
	VendorNVIDIA GPUVendor = "nvidia"
	VendorIntel  GPUVendor = "intel"
	VendorAMD    GPUVendor = "amd"
)

// HWProfile describes the GPU hardware capabilities detected at startup.
type HWProfile struct {
	GPUName     string    `json:"gpu_name"`
	Vendor      GPUVendor `json:"vendor"`
	DevicePath  string    `json:"device_path,omitempty"`
	VRAM_MB     int       `json:"vram_mb"`
	Encoders    []string  `json:"encoders"`
	Decoders    []string  `json:"decoders"`
	HWAccels    []string  `json:"hw_accels"`
	MaxSessions int       `json:"max_sessions"`
}

// DetectGPU probes the system for GPU capabilities.
// Cascading detection: NVIDIA → Intel → AMD → software-only.
func DetectGPU() HWProfile {
	if hw, ok := detectNVIDIA(); ok {
		return hw
	}
	if hw, ok := detectIntel(); ok {
		return hw
	}
	if hw, ok := detectAMD(); ok {
		return hw
	}
	return HWProfile{}
}

// detectNVIDIA probes for NVIDIA GPU via nvidia-smi and ffmpeg.
func detectNVIDIA() (HWProfile, bool) {
	hw := HWProfile{Vendor: VendorNVIDIA}

	out, err := runCmd("nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits")
	if err != nil {
		return HWProfile{}, false
	}

	parts := strings.SplitN(strings.TrimSpace(out), ", ", 2)
	if len(parts) == 2 {
		hw.GPUName = strings.TrimSpace(parts[0])
		if vram, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
			hw.VRAM_MB = vram
		}
	}

	// Query available hardware accelerators
	if out, err := runCmd("ffmpeg", "-hwaccels"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && line != "Hardware acceleration methods:" {
				hw.HWAccels = append(hw.HWAccels, line)
			}
		}
	}

	// Query available NVENC encoders
	hw.Encoders = probeEncodersByPattern("nvenc", "nv_")

	// Query available CUVID/NVDEC decoders
	if out, err := runCmd("ffmpeg", "-decoders"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "cuvid") || strings.Contains(line, "nv_") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					hw.Decoders = append(hw.Decoders, fields[1])
				}
			}
		}
	}

	hw.MaxSessions = maxSessionsForGPU(hw.GPUName, hw.Vendor)
	return hw, hw.GPUName != "" && len(hw.Encoders) > 0
}

// detectIntel probes for Intel GPU via vainfo looking for iHD or i965 driver.
func detectIntel() (HWProfile, bool) {
	out, err := runCmd("vainfo")
	if err != nil {
		return HWProfile{}, false
	}

	// Check for Intel driver
	if !strings.Contains(out, "iHD") && !strings.Contains(out, "i965") {
		return HWProfile{}, false
	}

	hw := HWProfile{
		Vendor:     VendorIntel,
		DevicePath: detectVAAPIDevice(),
	}

	// Parse GPU name from vainfo output
	hw.GPUName = parseVAInfoGPUName(out)

	// Query available hardware accelerators
	if hwaOut, err := runCmd("ffmpeg", "-hwaccels"); err == nil {
		for _, line := range strings.Split(hwaOut, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && line != "Hardware acceleration methods:" {
				hw.HWAccels = append(hw.HWAccels, line)
			}
		}
	}

	// Probe QSV and VAAPI encoders
	hw.Encoders = probeEncodersByPattern("_qsv", "_vaapi")

	// Probe QSV decoders
	if decOut, err := runCmd("ffmpeg", "-decoders"); err == nil {
		for _, line := range strings.Split(decOut, "\n") {
			if strings.Contains(line, "_qsv") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					hw.Decoders = append(hw.Decoders, fields[1])
				}
			}
		}
	}

	hw.MaxSessions = maxSessionsForGPU(hw.GPUName, hw.Vendor)
	return hw, hw.GPUName != "" && len(hw.Encoders) > 0
}

// detectAMD probes for AMD GPU via vainfo looking for radeonsi or AMDGPU driver.
func detectAMD() (HWProfile, bool) {
	out, err := runCmd("vainfo")
	if err != nil {
		return HWProfile{}, false
	}

	// Check for AMD driver
	if !strings.Contains(out, "radeonsi") && !strings.Contains(out, "AMDGPU") {
		return HWProfile{}, false
	}

	hw := HWProfile{
		Vendor:     VendorAMD,
		DevicePath: detectVAAPIDevice(),
	}

	// Parse GPU name from vainfo output
	hw.GPUName = parseVAInfoGPUName(out)

	// Query available hardware accelerators
	if hwaOut, err := runCmd("ffmpeg", "-hwaccels"); err == nil {
		for _, line := range strings.Split(hwaOut, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && line != "Hardware acceleration methods:" {
				hw.HWAccels = append(hw.HWAccels, line)
			}
		}
	}

	// Probe VAAPI encoders
	hw.Encoders = probeEncodersByPattern("_vaapi")

	// AMD uses VAAPI decoders (handled by ffmpeg's vaapi hwaccel, no explicit decoder needed)

	hw.MaxSessions = maxSessionsForGPU(hw.GPUName, hw.Vendor)
	return hw, hw.GPUName != "" && len(hw.Encoders) > 0
}

// detectVAAPIDevice finds the first DRI render node, falling back to /dev/dri/renderD128.
func detectVAAPIDevice() string {
	matches, err := filepath.Glob("/dev/dri/renderD*")
	if err == nil && len(matches) > 0 {
		return matches[0]
	}
	return "/dev/dri/renderD128"
}

// probeEncodersByPattern queries ffmpeg -encoders and returns encoders matching any pattern.
func probeEncodersByPattern(patterns ...string) []string {
	out, err := runCmd("ffmpeg", "-encoders")
	if err != nil {
		return nil
	}
	var encoders []string
	for _, line := range strings.Split(out, "\n") {
		for _, p := range patterns {
			if strings.Contains(line, p) {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					encoders = append(encoders, fields[1])
				}
				break
			}
		}
	}
	return encoders
}

// parseVAInfoGPUName extracts the GPU name from vainfo output.
// Looks for lines like "Driver version: Intel iHD driver ... - Intel(R) Xe Graphics"
func parseVAInfoGPUName(vainfo string) string {
	for _, line := range strings.Split(vainfo, "\n") {
		if strings.Contains(line, "Driver version") {
			// Try to extract a meaningful name after the last dash
			if idx := strings.LastIndex(line, " - "); idx >= 0 {
				name := strings.TrimSpace(line[idx+3:])
				if name != "" {
					return name
				}
			}
			// Fallback: return text after "Driver version:"
			marker := "Driver version:"
			if idx := strings.Index(line, marker); idx >= 0 {
				return strings.TrimSpace(line[idx+len(marker):])
			}
		}
	}
	return "Unknown GPU"
}

// HasEncoder returns true if the GPU supports the named encoder.
func (p HWProfile) HasEncoder(name string) bool {
	for _, e := range p.Encoders {
		if strings.EqualFold(e, name) {
			return true
		}
	}
	return false
}

// HasDecoder returns true if the GPU supports the named decoder.
func (p HWProfile) HasDecoder(name string) bool {
	for _, d := range p.Decoders {
		if strings.EqualFold(d, name) {
			return true
		}
	}
	return false
}

// HasHWAccel returns true if the named hardware accelerator is available.
func (p HWProfile) HasHWAccel(name string) bool {
	for _, a := range p.HWAccels {
		if strings.EqualFold(a, name) {
			return true
		}
	}
	return false
}

// IsGPUAvailable returns true if a GPU was detected with at least one encoder.
func (p HWProfile) IsGPUAvailable() bool {
	return p.GPUName != "" && len(p.Encoders) > 0
}

// maxSessionsForGPU returns the concurrent encoding session limit based on GPU vendor and model.
func maxSessionsForGPU(name string, vendor GPUVendor) int {
	switch vendor {
	case VendorIntel, VendorAMD:
		return 8
	case VendorNVIDIA:
		upper := strings.ToUpper(name)
		// Professional/datacenter GPUs — no session limit
		for _, prefix := range []string{"QUADRO", "TESLA", "A100", "A10", "A30", "A40", "L4", "L40", "H100", "H200", "B200"} {
			if strings.Contains(upper, prefix) {
				return 0 // unlimited
			}
		}
		// Consumer GPUs (GeForce, RTX, GTX, etc.)
		return 5
	default:
		return 5
	}
}

// runCmd executes a command with a 10-second timeout and returns stdout.
func runCmd(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	return string(out), err
}
