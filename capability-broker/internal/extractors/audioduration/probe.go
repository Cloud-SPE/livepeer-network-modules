// Package audioduration measures the playing time of an uploaded audio
// file so a transcription offering can bill by input duration.
//
// The measurement is deliberately seller-side and container-derived. The
// alternatives are worse: a duration the CALLER declares in a form field
// is self-reported by the party paying, and a duration read out of the
// backend's response body forces a response format on the caller —
// exactly the kind of request mutation gateways are removing.
package audioduration

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"
)

// ErrUnsupportedFormat means the bytes did not match any container this
// package can measure. Callers fall back to their configured default
// rather than guessing.
var ErrUnsupportedFormat = errors.New("audioduration: unrecognized audio container")

// ErrMalformed means the container was recognized but its duration
// fields were absent or inconsistent.
var ErrMalformed = errors.New("audioduration: container recognized but duration unreadable")

// Format is a recognized container.
type Format string

const (
	FormatWAV     Format = "wav"
	FormatFLAC    Format = "flac"
	FormatMP4     Format = "mp4" // also m4a
	FormatOgg     Format = "ogg" // Opus or Vorbis
	FormatMP3     Format = "mp3"
	FormatMatroic Format = "webm" // WebM / Matroska
)

// Result is a measured duration and how exact it is.
type Result struct {
	Duration time.Duration
	Format   Format
	// Exact is false when the duration was derived from a bitrate
	// estimate rather than a declared sample or frame count. Only
	// constant-bitrate MP3 without a Xing/VBRI header lands here.
	Exact bool
}

// Probe measures the duration of an audio file.
func Probe(b []byte) (Result, error) {
	switch {
	case hasPrefix(b, "RIFF") && len(b) >= 12 && string(b[8:12]) == "WAVE":
		return probeWAV(b)
	case hasPrefix(b, "fLaC"):
		return probeFLAC(b)
	case hasPrefix(b, "OggS"):
		return probeOgg(b)
	case len(b) >= 8 && string(b[4:8]) == "ftyp":
		return probeMP4(b)
	case len(b) >= 4 && b[0] == 0x1A && b[1] == 0x45 && b[2] == 0xDF && b[3] == 0xA3:
		return probeMatroska(b)
	case hasPrefix(b, "ID3") || isMP3FrameSync(b):
		return probeMP3(b)
	}
	return Result{}, ErrUnsupportedFormat
}

func hasPrefix(b []byte, s string) bool {
	return len(b) >= len(s) && string(b[:len(s)]) == s
}

func secondsToDuration(sec float64) (time.Duration, error) {
	if math.IsNaN(sec) || math.IsInf(sec, 0) || sec < 0 {
		return 0, ErrMalformed
	}
	// A duration beyond a day is not a transcription input; it is a
	// misparse, and billing one would be a large silent overcharge.
	if sec > 24*60*60 {
		return 0, fmt.Errorf("%w: implausible duration %.0fs", ErrMalformed, sec)
	}
	return time.Duration(sec * float64(time.Second)), nil
}

// ---------------------------------------------------------------------------
// WAV — RIFF chunks; duration is the data chunk over the byte rate.

func probeWAV(b []byte) (Result, error) {
	var byteRate uint32
	var dataSize uint32
	var sawFmt, sawData bool

	pos := 12 // past "RIFF" + size + "WAVE"
	for pos+8 <= len(b) {
		id := string(b[pos : pos+4])
		size := binary.LittleEndian.Uint32(b[pos+4 : pos+8])
		body := pos + 8
		switch id {
		case "fmt ":
			if body+16 > len(b) {
				return Result{}, ErrMalformed
			}
			byteRate = binary.LittleEndian.Uint32(b[body+8 : body+12])
			sawFmt = true
		case "data":
			dataSize = size
			// A streamed WAV can declare size 0 and run to EOF; measure
			// what actually arrived rather than trusting the header.
			if dataSize == 0 || body+int(dataSize) > len(b) {
				dataSize = uint32(len(b) - body)
			}
			sawData = true
		}
		if sawFmt && sawData {
			break
		}
		// Chunks are word-aligned.
		adv := int(size) + 8
		if size%2 == 1 {
			adv++
		}
		if adv <= 8 {
			return Result{}, ErrMalformed
		}
		pos += adv
	}
	if !sawFmt || !sawData || byteRate == 0 {
		return Result{}, ErrMalformed
	}
	d, err := secondsToDuration(float64(dataSize) / float64(byteRate))
	if err != nil {
		return Result{}, err
	}
	return Result{Duration: d, Format: FormatWAV, Exact: true}, nil
}

// ---------------------------------------------------------------------------
// FLAC — STREAMINFO carries total samples and sample rate outright.

func probeFLAC(b []byte) (Result, error) {
	// 4 magic + block header (1 type/last + 3 length) + 34-byte STREAMINFO
	if len(b) < 4+4+34 {
		return Result{}, ErrMalformed
	}
	if b[4]&0x7F != 0 { // first block must be STREAMINFO
		return Result{}, ErrMalformed
	}
	si := b[8 : 8+34]
	// After the block/frame sizes, a 64-bit field packs
	// sampleRate(20) channels(3) bitsPerSample(5) totalSamples(36).
	packed := binary.BigEndian.Uint64(si[10:18])
	sampleRate := uint32(packed >> 44)
	totalSamples := packed & 0xFFFFFFFFF // low 36 bits
	if sampleRate == 0 {
		return Result{}, ErrMalformed
	}
	if totalSamples == 0 {
		// Legal, and means "unknown" — a stream encoded without a
		// known length. Refuse rather than bill zero for real audio.
		return Result{}, fmt.Errorf("%w: FLAC declares no sample count", ErrMalformed)
	}
	d, err := secondsToDuration(float64(totalSamples) / float64(sampleRate))
	if err != nil {
		return Result{}, err
	}
	return Result{Duration: d, Format: FormatFLAC, Exact: true}, nil
}

// ---------------------------------------------------------------------------
// MP4 / M4A — moov→mvhd carries a timescale and a duration in it.

func probeMP4(b []byte) (Result, error) {
	moov, ok := findBox(b, "moov")
	if !ok {
		return Result{}, fmt.Errorf("%w: no moov box", ErrMalformed)
	}
	mvhd, ok := findBox(moov, "mvhd")
	if !ok {
		return Result{}, fmt.Errorf("%w: no mvhd box", ErrMalformed)
	}
	if len(mvhd) < 4 {
		return Result{}, ErrMalformed
	}
	var timescale uint32
	var dur uint64
	switch mvhd[0] { // version
	case 0:
		// version+flags(4) creation(4) modification(4) timescale(4) duration(4)
		if len(mvhd) < 20 {
			return Result{}, ErrMalformed
		}
		timescale = binary.BigEndian.Uint32(mvhd[12:16])
		dur = uint64(binary.BigEndian.Uint32(mvhd[16:20]))
	case 1:
		// version+flags(4) creation(8) modification(8) timescale(4) duration(8)
		if len(mvhd) < 32 {
			return Result{}, ErrMalformed
		}
		timescale = binary.BigEndian.Uint32(mvhd[20:24])
		dur = binary.BigEndian.Uint64(mvhd[24:32])
	default:
		return Result{}, fmt.Errorf("%w: mvhd version %d", ErrMalformed, mvhd[0])
	}
	if timescale == 0 {
		return Result{}, ErrMalformed
	}
	// A fragmented MP4 declares duration 0 in mvhd and carries the real
	// length in fragments. Refusing beats billing zero for real audio.
	if dur == 0 {
		return Result{}, fmt.Errorf("%w: mvhd duration is 0 (fragmented MP4?)", ErrMalformed)
	}
	d, err := secondsToDuration(float64(dur) / float64(timescale))
	if err != nil {
		return Result{}, err
	}
	return Result{Duration: d, Format: FormatMP4, Exact: true}, nil
}

// findBox returns the payload of the first top-level box with the given
// type, searching one level deep from the start of b.
func findBox(b []byte, want string) ([]byte, bool) {
	pos := 0
	for pos+8 <= len(b) {
		size := uint64(binary.BigEndian.Uint32(b[pos : pos+4]))
		typ := string(b[pos+4 : pos+8])
		body := pos + 8
		switch size {
		case 0:
			size = uint64(len(b) - pos) // to EOF
		case 1:
			// 64-bit size follows the type.
			if body+8 > len(b) {
				return nil, false
			}
			size = binary.BigEndian.Uint64(b[body : body+8])
			body += 8
		}
		if size < 8 || pos+int(size) > len(b) {
			// Truncated upload: the last box may run past what arrived.
			if typ == want && body < len(b) {
				return b[body:], true
			}
			return nil, false
		}
		if typ == want {
			return b[body : pos+int(size)], true
		}
		pos += int(size)
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// Ogg — the last page's granule position is the sample count; the rate
// comes from the identification header (Opus is always 48 kHz).

func probeOgg(b []byte) (Result, error) {
	rate, ok := oggSampleRate(b)
	if !ok {
		return Result{}, fmt.Errorf("%w: no Opus or Vorbis identification header", ErrMalformed)
	}
	granule, ok := oggLastGranule(b)
	if !ok {
		return Result{}, fmt.Errorf("%w: no readable Ogg page", ErrMalformed)
	}
	d, err := secondsToDuration(float64(granule) / float64(rate))
	if err != nil {
		return Result{}, err
	}
	return Result{Duration: d, Format: FormatOgg, Exact: true}, nil
}

func oggSampleRate(b []byte) (uint32, bool) {
	// The identification header sits in the first page's payload.
	if i := indexOf(b, []byte("OpusHead")); i >= 0 {
		// Opus granule positions are ALWAYS at 48 kHz regardless of the
		// original rate, which is the input-sample-rate field at +12.
		return 48000, true
	}
	if i := indexOf(b, []byte("\x01vorbis")); i >= 0 && i+16 <= len(b) {
		// version(4) channels(1) sampleRate(4) after the 7-byte magic.
		return binary.LittleEndian.Uint32(b[i+12 : i+16]), true
	}
	return 0, false
}

func oggLastGranule(b []byte) (uint64, bool) {
	var granule uint64
	found := false
	for i := 0; i+27 <= len(b); {
		if string(b[i:i+4]) != "OggS" {
			i++
			continue
		}
		g := binary.LittleEndian.Uint64(b[i+6 : i+14])
		segCount := int(b[i+26])
		if i+27+segCount > len(b) {
			break
		}
		payload := 0
		for _, n := range b[i+27 : i+27+segCount] {
			payload += int(n)
		}
		// -1 marks a page whose packet does not complete here; it is not
		// a position.
		if g != math.MaxUint64 {
			granule = g
			found = true
		}
		i += 27 + segCount + payload
	}
	return granule, found
}

func indexOf(haystack, needle []byte) int {
	n := len(needle)
	if n == 0 || len(haystack) < n {
		return -1
	}
	for i := 0; i+n <= len(haystack); i++ {
		if string(haystack[i:i+n]) == string(needle) {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// MP3 — no container duration. A Xing/Info or VBRI header gives an exact
// frame count; without one, a constant-bitrate estimate is the best
// available and is reported as inexact.

var mp3BitrateV1L3 = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
var mp3BitrateV2L3 = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
var mp3SampleRates = [4][4]int{
	{11025, 12000, 8000, 0},  // MPEG 2.5
	{0, 0, 0, 0},             // reserved
	{22050, 24000, 16000, 0}, // MPEG 2
	{44100, 48000, 32000, 0}, // MPEG 1
}

func isMP3FrameSync(b []byte) bool {
	return len(b) >= 2 && b[0] == 0xFF && b[1]&0xE0 == 0xE0
}

func probeMP3(b []byte) (Result, error) {
	audio := b
	if hasPrefix(b, "ID3") {
		if len(b) < 10 {
			return Result{}, ErrMalformed
		}
		// Syncsafe 28-bit size, 7 bits per byte.
		size := int(b[6]&0x7F)<<21 | int(b[7]&0x7F)<<14 | int(b[8]&0x7F)<<7 | int(b[9]&0x7F)
		off := 10 + size
		if off >= len(b) {
			return Result{}, fmt.Errorf("%w: ID3 tag covers the whole upload", ErrMalformed)
		}
		audio = b[off:]
	}
	start := -1
	for i := 0; i+4 <= len(audio) && i < 1<<16; i++ {
		if isMP3FrameSync(audio[i:]) {
			start = i
			break
		}
	}
	if start < 0 {
		return Result{}, fmt.Errorf("%w: no MP3 frame sync", ErrMalformed)
	}
	audio = audio[start:]

	h, err := parseMP3Header(audio)
	if err != nil {
		return Result{}, err
	}

	// Xing/Info (CBR-or-VBR frame count) or VBRI. Both live inside the
	// first frame, at an offset that depends on version and channels.
	if n, ok := mp3FrameCount(audio, h); ok {
		samplesPerFrame := 1152
		if h.version != mpegV1 {
			samplesPerFrame = 576
		}
		d, err := secondsToDuration(float64(n) * float64(samplesPerFrame) / float64(h.sampleRate))
		if err != nil {
			return Result{}, err
		}
		return Result{Duration: d, Format: FormatMP3, Exact: true}, nil
	}

	// Constant-bitrate estimate. Wrong for a VBR file with no header,
	// which is why it is flagged inexact rather than passed off as a
	// measurement.
	if h.bitrateKbps == 0 {
		return Result{}, fmt.Errorf("%w: MP3 declares a free-format bitrate", ErrMalformed)
	}
	sec := float64(len(audio)) * 8 / float64(h.bitrateKbps*1000)
	d, err := secondsToDuration(sec)
	if err != nil {
		return Result{}, err
	}
	return Result{Duration: d, Format: FormatMP3, Exact: false}, nil
}

type mpegVersion int

const (
	mpegV25 mpegVersion = iota
	mpegReserved
	mpegV2
	mpegV1
)

type mp3Header struct {
	version     mpegVersion
	sampleRate  int
	bitrateKbps int
	channelMode int
}

func parseMP3Header(b []byte) (mp3Header, error) {
	if len(b) < 4 {
		return mp3Header{}, ErrMalformed
	}
	ver := mpegVersion((b[1] >> 3) & 0x03)
	layer := (b[1] >> 1) & 0x03
	if ver == mpegReserved || layer == 0 {
		return mp3Header{}, fmt.Errorf("%w: reserved MPEG version or layer", ErrMalformed)
	}
	rateIdx := (b[2] >> 2) & 0x03
	sampleRate := mp3SampleRates[ver][rateIdx]
	if sampleRate == 0 {
		return mp3Header{}, fmt.Errorf("%w: reserved sample rate", ErrMalformed)
	}
	brIdx := (b[2] >> 4) & 0x0F
	br := mp3BitrateV2L3[brIdx]
	if ver == mpegV1 {
		br = mp3BitrateV1L3[brIdx]
	}
	return mp3Header{
		version:     ver,
		sampleRate:  sampleRate,
		bitrateKbps: br,
		channelMode: int((b[3] >> 6) & 0x03),
	}, nil
}

// mp3FrameCount reads the frame count from a Xing/Info or VBRI header.
func mp3FrameCount(frame []byte, h mp3Header) (uint32, bool) {
	// VBRI always sits 32 bytes after the frame header.
	if len(frame) >= 36+8 && string(frame[36:40]) == "VBRI" {
		return binary.BigEndian.Uint32(frame[36+14 : 36+18]), true
	}
	off := 4 + 32 // MPEG1 stereo
	if h.version == mpegV1 {
		if h.channelMode == 3 { // mono
			off = 4 + 17
		}
	} else {
		off = 4 + 17
		if h.channelMode == 3 {
			off = 4 + 9
		}
	}
	if off+8 > len(frame) {
		return 0, false
	}
	tag := string(frame[off : off+4])
	if tag != "Xing" && tag != "Info" {
		return 0, false
	}
	flags := binary.BigEndian.Uint32(frame[off+4 : off+8])
	if flags&0x1 == 0 { // no frame-count field
		return 0, false
	}
	if off+12 > len(frame) {
		return 0, false
	}
	return binary.BigEndian.Uint32(frame[off+8 : off+12]), true
}

// ErrInexact means the container was recognized and its duration is a
// bitrate estimate rather than a declared sample or frame count. Only
// constant-bitrate MP3 without a Xing/VBRI header reaches this.
var ErrInexact = errors.New("audioduration: duration is an estimate, not a measurement")

// EstimateCeilingSeconds is the multipart-audio-duration/v1 estimator
// contract: an exact whole-second ceiling, or an error.
//
// This is the shared surface — a client computes a funding ceiling with
// it before the work runs, and the broker bills from the same parse
// afterwards. The two must agree, or the ceiling is exceeded by the
// settlement and a correct exchange is refused.
//
// It refuses an inexact duration rather than returning one, which is the
// difference between this and Probe. A ceiling is a number somebody
// funds against: an estimate that reads slightly low underfunds real
// work, and one that reads high overcharges. Neither is a thing to guess
// at, so a duration that cannot be measured is not offered.
//
// Rounds UP. A rule that could return 0 for delivered work funds nothing
// for it.
func EstimateCeilingSeconds(b []byte) (int64, error) {
	res, err := Probe(b)
	if err != nil {
		return 0, err
	}
	if !res.Exact {
		return 0, fmt.Errorf("%w: %s", ErrInexact, res.Format)
	}
	return int64(math.Ceil(res.Duration.Seconds())), nil
}
