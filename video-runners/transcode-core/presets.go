package transcode

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Preset defines a transcoding preset configuration.
type Preset struct {
	Name            string `yaml:"name" json:"name"`
	Description     string `yaml:"description" json:"description"`
	VideoCodec      string `yaml:"video_codec" json:"video_codec"`
	AudioCodec      string `yaml:"audio_codec" json:"audio_codec"`
	Width           int    `yaml:"width" json:"width"`
	Height          int    `yaml:"height" json:"height"`
	Bitrate         string `yaml:"bitrate" json:"bitrate"`
	MaxRate         string `yaml:"max_rate" json:"max_rate"`
	BufSize         string `yaml:"buf_size" json:"buf_size"`
	CRF             int    `yaml:"crf,omitempty" json:"crf,omitempty"`
	FPS             int    `yaml:"fps,omitempty" json:"fps,omitempty"`
	PixFmt          string `yaml:"pix_fmt" json:"pix_fmt"`
	Profile         string `yaml:"profile,omitempty" json:"profile,omitempty"`
	Tier            string `yaml:"tier,omitempty" json:"tier,omitempty"`
	GPURequired     bool   `yaml:"gpu_required" json:"gpu_required"`
	AudioBitrate    string `yaml:"audio_bitrate" json:"audio_bitrate"`
	AudioChannels   int    `yaml:"audio_channels" json:"audio_channels"`
	AudioSampleRate int    `yaml:"audio_sample_rate,omitempty" json:"audio_sample_rate,omitempty"`
}

// PresetFile is the top-level structure for the presets YAML file.
type PresetFile struct {
	Presets []Preset `yaml:"presets"`
}

// LoadPresetsFromBytes parses a YAML byte slice into a list of presets.
func LoadPresetsFromBytes(data []byte) ([]Preset, error) {
	var pf PresetFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parse presets YAML: %w", err)
	}
	if len(pf.Presets) == 0 {
		return nil, fmt.Errorf("no presets found in YAML")
	}
	return pf.Presets, nil
}

// ValidatePresets filters presets against the detected hardware profile.
// Returns the valid presets and a list of skipped preset names.
func ValidatePresets(presets []Preset, hw HWProfile) (valid []Preset, skipped []string) {
	for _, p := range presets {
		if p.GPURequired && !hw.IsGPUAvailable() {
			skipped = append(skipped, p.Name)
			continue
		}
		if p.GPURequired {
			encoder := EncoderForCodec(p.VideoCodec, hw)
			if encoder == softwareEncoderForCodec(p.VideoCodec) {
				// GPU required but only software encoder available
				skipped = append(skipped, p.Name)
				continue
			}
		}
		valid = append(valid, p)
	}
	return
}

// FindPreset looks up a preset by name (case-insensitive).
func FindPreset(presets []Preset, name string) (Preset, bool) {
	for _, p := range presets {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Preset{}, false
}

// EncoderForCodec returns the best available encoder for the given codec,
// preferring GPU-accelerated encoders: nvenc > qsv > vaapi > software.
func EncoderForCodec(codec string, hw HWProfile) string {
	switch strings.ToLower(codec) {
	case "h264", "avc":
		if hw.HasEncoder("h264_nvenc") {
			return "h264_nvenc"
		}
		if hw.HasEncoder("h264_qsv") {
			return "h264_qsv"
		}
		if hw.HasEncoder("h264_vaapi") {
			return "h264_vaapi"
		}
		return "libx264"
	case "h265", "hevc":
		if hw.HasEncoder("hevc_nvenc") {
			return "hevc_nvenc"
		}
		if hw.HasEncoder("hevc_qsv") {
			return "hevc_qsv"
		}
		if hw.HasEncoder("hevc_vaapi") {
			return "hevc_vaapi"
		}
		return "libx265"
	case "av1":
		if hw.HasEncoder("av1_nvenc") {
			return "av1_nvenc"
		}
		if hw.HasEncoder("av1_qsv") {
			return "av1_qsv"
		}
		if hw.HasEncoder("av1_vaapi") {
			return "av1_vaapi"
		}
		return "libsvtav1"
	case "vp9":
		if hw.HasEncoder("vp9_vaapi") {
			return "vp9_vaapi"
		}
		return "libvpx-vp9"
	default:
		return codec
	}
}

// softwareEncoderForCodec returns the software-only encoder for a codec.
func softwareEncoderForCodec(codec string) string {
	switch strings.ToLower(codec) {
	case "h264", "avc":
		return "libx264"
	case "h265", "hevc":
		return "libx265"
	case "av1":
		return "libsvtav1"
	case "vp9":
		return "libvpx-vp9"
	default:
		return codec
	}
}

// DecoderForCodec returns the best available decoder for the given codec,
// preferring GPU-accelerated decoders: cuvid > qsv > default.
func DecoderForCodec(codec string, hw HWProfile) string {
	switch strings.ToLower(codec) {
	case "h264", "avc":
		if hw.HasDecoder("h264_cuvid") {
			return "h264_cuvid"
		}
		if hw.HasDecoder("h264_qsv") {
			return "h264_qsv"
		}
		return ""
	case "h265", "hevc":
		if hw.HasDecoder("hevc_cuvid") {
			return "hevc_cuvid"
		}
		if hw.HasDecoder("hevc_qsv") {
			return "hevc_qsv"
		}
		return ""
	case "av1":
		if hw.HasDecoder("av1_cuvid") {
			return "av1_cuvid"
		}
		if hw.HasDecoder("av1_qsv") {
			return "av1_qsv"
		}
		return ""
	case "vp9":
		if hw.HasDecoder("vp9_cuvid") {
			return "vp9_cuvid"
		}
		if hw.HasDecoder("vp9_qsv") {
			return "vp9_qsv"
		}
		return ""
	default:
		return ""
	}
}
