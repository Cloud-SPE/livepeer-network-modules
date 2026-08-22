// genfixtures writes the shared multipart-audio-duration/v1 conformance
// fixtures: container bytes plus a manifest of expected results.
//
// They exist because there is now more than one implementation of this
// estimator — the broker's, which bills, and a client's, which sets a
// funding ceiling before the work runs. Two parsers that disagree
// produce a ceiling the settlement then exceeds, and that shows up as a
// refused exchange rather than as a bug report. Both sides run these, so
// the contract is the bytes rather than the prose.
//
//	go run ./internal/extractors/audioduration/genfixtures -out <dir>
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

type fixture struct {
	File    string  `json:"file"`
	Format  string  `json:"format,omitempty"`
	Seconds float64 `json:"seconds,omitempty"`
	// CeilingSeconds is what a funding ceiling must be: seconds rounded
	// UP. A rule that could round down funds less work than the exchange
	// delivers.
	CeilingSeconds int64 `json:"ceiling_seconds,omitempty"`
	// Reject means the estimator MUST refuse. Refusing is not the same
	// as returning zero: zero claims the audio has no duration.
	Reject bool   `json:"reject"`
	Why    string `json:"why"`
}

func main() {
	out := flag.String("out", ".", "directory to write fixtures into")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		panic(err)
	}

	var manifest []fixture
	write := func(name string, body []byte, f fixture) {
		if err := os.WriteFile(filepath.Join(*out, name), body, 0o644); err != nil {
			panic(err)
		}
		f.File = name
		manifest = append(manifest, f)
	}

	write("wav-16k-mono-3s.wav", wavFixture(16000, 1, 16, 48000), fixture{
		Format: "wav", Seconds: 3, CeilingSeconds: 3,
		Why: "data chunk over the fmt byte rate",
	})
	write("wav-16k-mono-0.4s.wav", wavFixture(16000, 1, 16, 6400), fixture{
		Format: "wav", Seconds: 0.4, CeilingSeconds: 1,
		Why: "rounds UP: a rule returning 0 for delivered work funds nothing",
	})
	write("wav-44k-stereo-1.5s.wav", wavFixture(44100, 2, 16, 66150), fixture{
		Format: "wav", Seconds: 1.5, CeilingSeconds: 2,
		Why: "stereo byte rate; the ceiling is still whole seconds",
	})
	write("flac-44k-7s.flac", flacFixture(44100, 44100*7), fixture{
		Format: "flac", Seconds: 7, CeilingSeconds: 7,
		Why: "STREAMINFO total samples over sample rate",
	})
	write("mp4-12.5s.m4a", mp4Fixture(1000, 12500), fixture{
		Format: "mp4", Seconds: 12.5, CeilingSeconds: 13,
		Why: "mvhd duration over timescale",
	})
	write("webm-9.5s.webm", webmFixture(1000000, 9500), fixture{
		Format: "webm", Seconds: 9.5, CeilingSeconds: 10,
		Why: "Info Duration times TimecodeScale",
	})

	// MP3 with a Xing header declares a frame count, so the duration is
	// exact and fundable.
	write("mp3-xing-4s.mp3", mp3Xing(44100, 153), fixture{
		Format: "mp3", Seconds: 3.996734693877551, CeilingSeconds: 4,
		Why: "Xing frame count times samples per frame over sample rate",
	})
	// And without one it is a constant-bitrate GUESS, which an estimator
	// must refuse rather than fund. This is the case the gateway team
	// called out by name: a CBR estimate on a VBR file can be far out,
	// and a ceiling built on it either underfunds real work or overcharges.
	write("mp3-headerless.mp3", mp3Headerless(44100), fixture{
		Reject: true,
		Why: "headerless MP3 duration is a bitrate estimate, never exact; an estimator " +
			"MUST refuse rather than silently guess",
	})

	write("flac-unknown-length.flac", flacFixture(44100, 0), fixture{
		Reject: true,
		Why:    "FLAC declaring no sample count; billing zero for real audio is wrong",
	})
	write("mp4-fragmented.m4a", mp4Fixture(1000, 0), fixture{
		Reject: true,
		Why:    "fragmented MP4 declares duration 0 in mvhd",
	})
	write("not-audio.bin", []byte("this is not an audio container at all"), fixture{
		Reject: true,
		Why:    "unrecognized container",
	})
	write("flac-implausible.flac", flacFixture(44100, uint64(44100)*25*3600), fixture{
		Reject: true,
		Why: "25 hours is a misparse rather than a transcription input; billing it would " +
			"be a large silent overcharge",
	})
	write("truncated.wav", wavFixture(16000, 1, 16, 48000)[:20], fixture{
		Reject: true,
		Why:    "truncated header",
	})

	raw, err := json.MarshalIndent(map[string]any{
		"estimator": "multipart-audio-duration/v1",
		"rounding":  "ceil-to-whole-seconds",
		"contract": "An implementation MUST return exactly ceiling_seconds for every fixture " +
			"with reject=false, and MUST refuse every fixture with reject=true. Refusing " +
			"is not the same as returning zero.",
		"fixtures": manifest,
	}, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(*out, "manifest.json"), append(raw, '\n'), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote %d fixtures + manifest to %s\n", len(manifest), *out)
}

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

func flacFixture(sampleRate int, totalSamples uint64) []byte {
	var b bytes.Buffer
	b.WriteString("fLaC")
	b.WriteByte(0x00)
	b.Write([]byte{0, 0, 34})
	si := make([]byte, 34)
	packed := uint64(sampleRate)<<44 | uint64(0)<<41 | uint64(15)<<36 | totalSamples
	binary.BigEndian.PutUint64(si[10:18], packed)
	b.Write(si)
	return b.Bytes()
}

func mp4Fixture(timescale, duration uint32) []byte {
	mvhd := new(bytes.Buffer)
	mvhd.Write([]byte{0, 0, 0, 0})
	mvhd.Write(make([]byte, 8))
	_ = binary.Write(mvhd, binary.BigEndian, timescale)
	_ = binary.Write(mvhd, binary.BigEndian, duration)
	return append(box("ftyp", []byte("M4A isom")), box("moov", box("mvhd", mvhd.Bytes()))...)
}

func box(typ string, payload []byte) []byte {
	out := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(payload)))
	copy(out[4:8], typ)
	copy(out[8:], payload)
	return out
}

func webmFixture(timecodeScaleNs uint64, durationTicks float64) []byte {
	var info bytes.Buffer
	info.Write([]byte{0x2A, 0xD7, 0xB1})
	scale := make([]byte, 8)
	binary.BigEndian.PutUint64(scale, timecodeScaleNs)
	info.WriteByte(0x88)
	info.Write(scale)
	info.Write([]byte{0x44, 0x89})
	info.WriteByte(0x88)
	dur := make([]byte, 8)
	binary.BigEndian.PutUint64(dur, math.Float64bits(durationTicks))
	info.Write(dur)
	infoBox := ebmlElem([]byte{0x15, 0x49, 0xA9, 0x66}, info.Bytes())
	segBox := ebmlElem([]byte{0x18, 0x53, 0x80, 0x67}, infoBox)
	header := []byte{0x1A, 0x45, 0xDF, 0xA3, 0x84, 0x00, 0x00, 0x00, 0x00}
	return append(header, segBox...)
}

func ebmlElem(id, payload []byte) []byte {
	out := append([]byte(nil), id...)
	size := make([]byte, 8)
	binary.BigEndian.PutUint64(size, uint64(len(payload)))
	size[0] = 0x01
	out = append(out, size...)
	return append(out, payload...)
}

// mp3Frame builds one MPEG-1 Layer III frame header at 128kbps.
func mp3Frame(sampleRate int) []byte {
	h := []byte{0xFF, 0xFB, 0x90, 0x00} // MPEG1 L3, 128kbps, 44.1kHz, stereo
	if sampleRate != 44100 {
		panic("only 44100 modelled")
	}
	return h
}

// mp3Xing builds a frame carrying a Xing header with a frame count, so
// the duration is declared rather than estimated.
func mp3Xing(sampleRate int, frames uint32) []byte {
	var b bytes.Buffer
	b.Write(mp3Frame(sampleRate))
	// MPEG1 stereo: the Xing tag sits 32 bytes past the 4-byte header.
	b.Write(make([]byte, 32))
	b.WriteString("Xing")
	_ = binary.Write(&b, binary.BigEndian, uint32(0x1)) // flags: frame count present
	_ = binary.Write(&b, binary.BigEndian, frames)
	b.Write(make([]byte, 64))
	return b.Bytes()
}

// mp3Headerless builds frames with no Xing/VBRI, so any duration is a
// bitrate estimate.
func mp3Headerless(sampleRate int) []byte {
	var b bytes.Buffer
	for i := 0; i < 40; i++ {
		b.Write(mp3Frame(sampleRate))
		b.Write(make([]byte, 413)) // 128kbps frame body at 44.1kHz
	}
	return b.Bytes()
}
