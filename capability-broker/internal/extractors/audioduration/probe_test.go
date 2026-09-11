package audioduration

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// Fixtures are synthesized headers rather than real recordings: the
// parser reads declared structure, so a header with the right fields
// exercises exactly what production exercises, and the numbers are known
// rather than approximately known.

func wavFixture(sampleRate, channels, bitsPerSample, frames int) []byte {
	byteRate := sampleRate * channels * bitsPerSample / 8
	dataSize := frames * channels * bitsPerSample / 8
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+dataSize))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&b, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&b, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels*bitsPerSample/8))
	_ = binary.Write(&b, binary.LittleEndian, uint16(bitsPerSample))
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(dataSize))
	b.Write(make([]byte, dataSize))
	return b.Bytes()
}

func TestProbeWAV(t *testing.T) {
	// 16 kHz mono 16-bit, 48000 frames = exactly 3 seconds.
	got, err := Probe(wavFixture(16000, 1, 16, 48000))
	if err != nil {
		t.Fatal(err)
	}
	if got.Format != FormatWAV || !got.Exact {
		t.Fatalf("format=%s exact=%v", got.Format, got.Exact)
	}
	if got.Duration != 3*time.Second {
		t.Fatalf("duration = %s; want 3s", got.Duration)
	}
}

// A streamed WAV can declare a zero-length data chunk and run to EOF.
// Trusting the header there bills zero for real audio.
func TestProbeWAVStreamedZeroDataSize(t *testing.T) {
	b := wavFixture(16000, 1, 16, 48000)
	// Zero the declared data size, keeping the payload.
	copy(b[40:44], []byte{0, 0, 0, 0})
	got, err := Probe(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Duration != 3*time.Second {
		t.Fatalf("duration = %s; want 3s measured from what arrived", got.Duration)
	}
}

func flacFixture(sampleRate int, totalSamples uint64) []byte {
	var b bytes.Buffer
	b.WriteString("fLaC")
	b.WriteByte(0x00) // last=0, type=STREAMINFO
	b.Write([]byte{0, 0, 34})
	si := make([]byte, 34)
	packed := uint64(sampleRate)<<44 | uint64(0)<<41 | uint64(15)<<36 | totalSamples
	binary.BigEndian.PutUint64(si[10:18], packed)
	b.Write(si)
	return b.Bytes()
}

func TestProbeFLAC(t *testing.T) {
	got, err := Probe(flacFixture(44100, 44100*7))
	if err != nil {
		t.Fatal(err)
	}
	if got.Format != FormatFLAC || got.Duration != 7*time.Second {
		t.Fatalf("format=%s duration=%s; want flac 7s", got.Format, got.Duration)
	}
}

// A FLAC encoded without a known length declares zero samples. Billing
// zero for real audio is the wrong answer; refusing is the right one.
func TestProbeFLACUnknownLengthRefuses(t *testing.T) {
	if _, err := Probe(flacFixture(44100, 0)); err == nil {
		t.Fatal("expected an error for a FLAC with no declared sample count")
	}
}

func mp4Fixture(timescale uint32, duration uint32) []byte {
	mvhd := new(bytes.Buffer)
	mvhd.Write([]byte{0, 0, 0, 0}) // version 0 + flags
	mvhd.Write(make([]byte, 8))    // creation + modification
	_ = binary.Write(mvhd, binary.BigEndian, timescale)
	_ = binary.Write(mvhd, binary.BigEndian, duration)
	mvhdBox := box("mvhd", mvhd.Bytes())
	moovBox := box("moov", mvhdBox)
	ftyp := box("ftyp", []byte("M4A isom"))
	return append(ftyp, moovBox...)
}

func box(typ string, payload []byte) []byte {
	out := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(payload)))
	copy(out[4:8], typ)
	copy(out[8:], payload)
	return out
}

func TestProbeMP4(t *testing.T) {
	got, err := Probe(mp4Fixture(1000, 12500)) // 12.5s at 1ms ticks
	if err != nil {
		t.Fatal(err)
	}
	if got.Format != FormatMP4 {
		t.Fatalf("format = %s", got.Format)
	}
	if got.Duration != 12500*time.Millisecond {
		t.Fatalf("duration = %s; want 12.5s", got.Duration)
	}
}

// A fragmented MP4 declares 0 in mvhd. Billing zero would be silent.
func TestProbeMP4FragmentedRefuses(t *testing.T) {
	if _, err := Probe(mp4Fixture(1000, 0)); err == nil {
		t.Fatal("expected an error for an mvhd duration of 0")
	}
}

func TestProbeUnsupportedFormat(t *testing.T) {
	if _, err := Probe([]byte("this is not audio at all")); err == nil {
		t.Fatal("expected ErrUnsupportedFormat")
	}
}

func TestProbeRejectsImplausibleDuration(t *testing.T) {
	// 25 hours: a misparse rather than a transcription input, and
	// billing it would be a large silent overcharge.
	if _, err := Probe(flacFixture(44100, uint64(44100)*25*3600)); err == nil {
		t.Fatal("expected an implausible-duration error")
	}
}

func webmFixture(timecodeScaleNs uint64, durationTicks float64) []byte {
	var info bytes.Buffer
	info.Write([]byte{0x2A, 0xD7, 0xB1}) // TimecodeScale id
	scale := make([]byte, 8)
	binary.BigEndian.PutUint64(scale, timecodeScaleNs)
	info.WriteByte(0x88) // size 8
	info.Write(scale)
	info.Write([]byte{0x44, 0x89}) // Duration id
	info.WriteByte(0x88)           // size 8
	dur := make([]byte, 8)
	binary.BigEndian.PutUint64(dur, math.Float64bits(durationTicks))
	info.Write(dur)

	infoBox := ebmlElem([]byte{0x15, 0x49, 0xA9, 0x66}, info.Bytes())
	segBox := ebmlElem([]byte{0x18, 0x53, 0x80, 0x67}, infoBox)
	header := []byte{0x1A, 0x45, 0xDF, 0xA3, 0x84, 0x00, 0x00, 0x00, 0x00} // EBML hdr
	return append(header, segBox...)
}

func ebmlElem(id, payload []byte) []byte {
	out := append([]byte(nil), id...)
	// 8-byte size form: 0x01 marker then 7 bytes.
	size := make([]byte, 8)
	size[0] = 0x01
	binary.BigEndian.PutUint64(size, uint64(len(payload)))
	size[0] = 0x01
	out = append(out, size...)
	return append(out, payload...)
}

func TestProbeWebM(t *testing.T) {
	// 1 ms ticks, 9500 ticks = 9.5 seconds.
	got, err := Probe(webmFixture(1000000, 9500))
	if err != nil {
		t.Fatal(err)
	}
	if got.Format != FormatMatroic {
		t.Fatalf("format = %s", got.Format)
	}
	if got.Duration != 9500*time.Millisecond {
		t.Fatalf("duration = %s; want 9.5s", got.Duration)
	}
}
