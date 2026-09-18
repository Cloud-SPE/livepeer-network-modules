package audioduration

import (
	"encoding/binary"
	"fmt"
	"math"
)

// WebM / Matroska — Segment→Info carries TimecodeScale (ns per tick,
// default 1,000,000) and Duration (a float in ticks).
//
// EBML is a tree of (variable-length id, variable-length size, payload)
// nodes. Only two containers need descending into, so this walks rather
// than building a general parser: a general one is a much larger surface
// for a job that is "find two scalars".
const (
	idSegment       = 0x18538067
	idInfo          = 0x1549A966
	idTimecodeScale = 0x2AD7B1
	idDuration      = 0x4489
)

func probeMatroska(b []byte) (Result, error) {
	seg, ok := ebmlFind(b, idSegment, true)
	if !ok {
		return Result{}, fmt.Errorf("%w: no Segment element", ErrMalformed)
	}
	info, ok := ebmlFind(seg, idInfo, true)
	if !ok {
		return Result{}, fmt.Errorf("%w: no Info element", ErrMalformed)
	}

	scaleNs := uint64(1000000) // spec default: 1 ms per tick
	if raw, ok := ebmlFind(info, idTimecodeScale, false); ok && len(raw) > 0 {
		scaleNs = ebmlUint(raw)
	}
	raw, ok := ebmlFind(info, idDuration, false)
	if !ok {
		// Live-recorded WebM often has no Duration until finalized.
		// Refusing beats billing zero for real audio.
		return Result{}, fmt.Errorf("%w: Info carries no Duration", ErrMalformed)
	}
	ticks, ok := ebmlFloat(raw)
	if !ok {
		return Result{}, fmt.Errorf("%w: Duration is not a float", ErrMalformed)
	}
	if scaleNs == 0 {
		return Result{}, ErrMalformed
	}
	sec := ticks * float64(scaleNs) / 1e9
	d, err := secondsToDuration(sec)
	if err != nil {
		return Result{}, err
	}
	return Result{Duration: d, Format: FormatMatroic, Exact: true}, nil
}

// ebmlFind scans one level for an element id and returns its payload.
func ebmlFind(b []byte, want uint32, _ bool) ([]byte, bool) {
	pos := 0
	for pos < len(b) {
		id, idLen, ok := ebmlID(b[pos:])
		if !ok {
			return nil, false
		}
		size, sizeLen, unknown, ok := ebmlSize(b[pos+idLen:])
		if !ok {
			return nil, false
		}
		body := pos + idLen + sizeLen
		if body > len(b) {
			return nil, false
		}
		end := len(b)
		if !unknown && body+int(size) <= len(b) {
			end = body + int(size)
		}
		// An element whose declared size overruns the buffer is a
		// truncated upload; measure what arrived rather than refusing.
		if id == want {
			return b[body:end], true
		}
		if end <= pos {
			return nil, false // no forward progress; malformed
		}
		pos = end
	}
	return nil, false
}

// ebmlID reads a class-A..D variable-length identifier. The marker bits
// are KEPT: spec ids are written with them (Segment is 0x18538067, not
// 0x08538067), so stripping would make every comparison miss.
func ebmlID(b []byte) (uint32, int, bool) {
	if len(b) == 0 {
		return 0, 0, false
	}
	n := ebmlLen(b[0])
	if n == 0 || n > 4 || len(b) < n {
		return 0, 0, false
	}
	var id uint32
	for i := 0; i < n; i++ {
		id = id<<8 | uint32(b[i])
	}
	return id, n, true
}

// ebmlSize reads a variable-length size, stripping the marker bit.
func ebmlSize(b []byte) (uint64, int, bool, bool) {
	if len(b) == 0 {
		return 0, 0, false, false
	}
	n := ebmlLen(b[0])
	if n == 0 || n > 8 || len(b) < n {
		return 0, 0, false, false
	}
	v := uint64(b[0]) & (0xFF >> uint(n))
	allOnes := v == (0xFF>>uint(n))&0xFF
	for i := 1; i < n; i++ {
		v = v<<8 | uint64(b[i])
		if b[i] != 0xFF {
			allOnes = false
		}
	}
	return v, n, allOnes, true
}

// ebmlLen returns how many bytes the variable-length integer starting
// with this byte occupies.
func ebmlLen(first byte) int {
	for i := 0; i < 8; i++ {
		if first&(0x80>>uint(i)) != 0 {
			return i + 1
		}
	}
	return 0
}

func ebmlUint(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

func ebmlFloat(b []byte) (float64, bool) {
	switch len(b) {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b))), true
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(b)), true
	}
	return 0, false
}
