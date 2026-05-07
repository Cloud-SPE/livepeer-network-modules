package transcode

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ProgressInfo holds parsed ffmpeg progress information from stderr output.
type ProgressInfo struct {
	Frame   int           `json:"frame"`
	FPS     float64       `json:"fps"`
	Size    string        `json:"size"`
	Time    time.Duration `json:"time_ns"`
	Bitrate string        `json:"bitrate"`
	Speed   float64       `json:"speed"`
}

var progressRe = regexp.MustCompile(
	`frame=\s*(\d+)\s+fps=\s*([\d.]+)\s+.*size=\s*(\S+)\s+time=\s*([\d:.]+)\s+bitrate=\s*(\S+)\s+speed=\s*([\d.]+)x`,
)

// ParseProgressLine attempts to parse an ffmpeg stderr progress line.
// Returns the parsed info and true if successful, or zero value and false if the line doesn't match.
func ParseProgressLine(line string) (ProgressInfo, bool) {
	m := progressRe.FindStringSubmatch(line)
	if m == nil {
		return ProgressInfo{}, false
	}

	frame, _ := strconv.Atoi(m[1])
	fps, _ := strconv.ParseFloat(m[2], 64)
	dur, _ := ParseDuration(m[4])
	speed, _ := strconv.ParseFloat(m[6], 64)

	return ProgressInfo{
		Frame:   frame,
		FPS:     fps,
		Size:    m[3],
		Time:    dur,
		Bitrate: m[5],
		Speed:   speed,
	}, true
}

// CalcPercent returns the encoding progress as a percentage (0.0-100.0).
func CalcPercent(current, total time.Duration) float64 {
	if total <= 0 {
		return 0
	}
	pct := float64(current) / float64(total) * 100.0
	if pct > 100.0 {
		return 100.0
	}
	if pct < 0 {
		return 0
	}
	return pct
}

// ParseDuration parses an ffmpeg time string "HH:MM:SS.ss" into a time.Duration.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return 0, fmt.Errorf("empty or N/A duration")
	}

	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid duration format: %s", s)
	}

	hours, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid hours in %s: %w", s, err)
	}

	minutes, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("invalid minutes in %s: %w", s, err)
	}

	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid seconds in %s: %w", s, err)
	}

	total := time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds*float64(time.Second))

	return total, nil
}
