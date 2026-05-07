package transcode

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ABRVideoSettings defines the video encoding parameters for one ABR rendition.
type ABRVideoSettings struct {
	Codec      string `yaml:"codec" json:"codec"`
	Width      int    `yaml:"width" json:"width"`
	Height     int    `yaml:"height" json:"height"`
	Bitrate    string `yaml:"bitrate" json:"bitrate"`
	MaxBitrate string `yaml:"max_bitrate" json:"max_bitrate"`
	BufSize    string `yaml:"buf_size,omitempty" json:"buf_size,omitempty"`
	Profile    string `yaml:"profile,omitempty" json:"profile,omitempty"`
	Level      string `yaml:"level,omitempty" json:"level,omitempty"`
	PixFmt     string `yaml:"pix_fmt,omitempty" json:"pix_fmt,omitempty"`
}

// ABRAudioSettings defines the audio encoding parameters for one ABR rendition.
type ABRAudioSettings struct {
	Codec      string `yaml:"codec" json:"codec"`
	Bitrate    string `yaml:"bitrate" json:"bitrate"`
	Channels   int    `yaml:"channels" json:"channels"`
	SampleRate int    `yaml:"sample_rate,omitempty" json:"sample_rate,omitempty"`
}

// ABRRendition defines a single rung in the ABR ladder.
type ABRRendition struct {
	Name  string            `yaml:"name" json:"name"`
	Video *ABRVideoSettings `yaml:"video,omitempty" json:"video,omitempty"`
	Audio ABRAudioSettings  `yaml:"audio" json:"audio"`
}

// ABRPreset defines a complete ABR ladder preset.
type ABRPreset struct {
	Name            string         `yaml:"name" json:"name"`
	Description     string         `yaml:"description" json:"description"`
	Type            string         `yaml:"type" json:"type"`
	Format          string         `yaml:"format" json:"format"`
	HLSMode         string         `yaml:"hls_mode" json:"hls_mode"`
	SegmentDuration int            `yaml:"segment_duration" json:"segment_duration"`
	Renditions      []ABRRendition `yaml:"renditions" json:"renditions"`
}

// ABRPresetFile is the top-level structure for the ABR presets YAML file.
type ABRPresetFile struct {
	Presets []ABRPreset `yaml:"presets"`
}

// LoadABRPresetsFromBytes parses a YAML byte slice into a list of ABR presets.
func LoadABRPresetsFromBytes(data []byte) ([]ABRPreset, error) {
	var pf ABRPresetFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parse ABR presets YAML: %w", err)
	}
	if len(pf.Presets) == 0 {
		return nil, fmt.Errorf("no ABR presets found in YAML")
	}
	for i, p := range pf.Presets {
		if len(p.Renditions) == 0 {
			return nil, fmt.Errorf("ABR preset %q has no renditions", p.Name)
		}
		if p.SegmentDuration <= 0 {
			pf.Presets[i].SegmentDuration = 6
		}
	}
	return pf.Presets, nil
}

// ValidateABRPresets filters ABR presets against detected hardware.
// A preset is valid if all its video renditions can be encoded by the GPU.
func ValidateABRPresets(presets []ABRPreset, hw HWProfile) (valid []ABRPreset, skipped []string) {
	for _, p := range presets {
		canEncode := true
		for _, r := range p.Renditions {
			if r.Video == nil {
				continue // audio-only rendition, always valid
			}
			if !hw.IsGPUAvailable() {
				canEncode = false
				break
			}
			encoder := EncoderForCodec(r.Video.Codec, hw)
			if encoder == softwareEncoderForCodec(r.Video.Codec) {
				canEncode = false
				break
			}
		}
		if canEncode {
			valid = append(valid, p)
		} else {
			skipped = append(skipped, p.Name)
		}
	}
	return
}

// FindABRPreset looks up an ABR preset by name (case-insensitive).
func FindABRPreset(presets []ABRPreset, name string) (ABRPreset, bool) {
	for _, p := range presets {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return ABRPreset{}, false
}

// RenditionNames returns the list of rendition names for an ABR preset.
func (p ABRPreset) RenditionNames() []string {
	names := make([]string, len(p.Renditions))
	for i, r := range p.Renditions {
		names[i] = r.Name
	}
	return names
}

// VideoRenditions returns only the renditions that have video settings.
func (p ABRPreset) VideoRenditions() []ABRRendition {
	var result []ABRRendition
	for _, r := range p.Renditions {
		if r.Video != nil {
			result = append(result, r)
		}
	}
	return result
}
