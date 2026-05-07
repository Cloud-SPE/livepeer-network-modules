package transcode

import (
	"fmt"
	"os/exec"
	"strconv"
)

// ThumbnailCmd builds an ffmpeg command to extract a single JPEG frame.
// Seeks to the given timestamp and outputs a JPEG at the specified resolution.
// If width and height are both 0, the original dimensions are used.
func ThumbnailCmd(inputPath, outputPath string, seekSeconds float64,
	width, height int, hw HWProfile) *exec.Cmd {

	args := []string{"-y"}

	// Seek before input for fast seeking
	if seekSeconds > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", seekSeconds))
	}

	args = append(args, "-i", inputPath)

	// Extract single frame
	args = append(args, "-vframes", "1")

	// JPEG quality (2 = high quality)
	args = append(args, "-q:v", "2")

	// Scale if dimensions specified
	if width > 0 || height > 0 {
		w := width
		h := height
		if w <= 0 {
			w = -2
		}
		if h <= 0 {
			h = -2
		}
		args = append(args, "-vf", "scale="+strconv.Itoa(w)+":"+strconv.Itoa(h))
	}

	args = append(args, outputPath)

	return exec.Command("ffmpeg", args...)
}

// ResolveSeekTime returns a valid seek time. If seekSeconds is 0 or negative,
// defaults to 10% of the total duration.
func ResolveSeekTime(seekSeconds float64, duration float64) float64 {
	if seekSeconds > 0 {
		// Clamp to duration
		if duration > 0 && seekSeconds >= duration {
			return duration * 0.9
		}
		return seekSeconds
	}
	if duration > 0 {
		return duration * 0.1
	}
	return 0
}
