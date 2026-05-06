// Package bytescounted implements the bytes-counted extractor per
// livepeer-network-protocol/extractors/bytes-counted.md.
//
// Tally on-wire bytes flowing through the broker (request, response, or
// both), divided by a configurable granularity to convert to work-units.
// HTTP headers excluded by default; opt in via `headers: true`.
//
// Plan 0015 adds a LiveCounter sibling: long-running mode drivers
// (ws-realtime, http-stream) increment a shared `atomic.Uint64` byte
// counter as data flows; the payment middleware polls that counter every
// tick to drive interim DebitBalance calls. The Extract path is unchanged.
package bytescounted

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

const Name = "bytes-counted"

type Extractor struct {
	direction   string // "request" | "response" | "both"
	granularity uint64
	headers     bool
}

var _ extractors.Extractor = (*Extractor)(nil)

func New(cfg map[string]any) (extractors.Extractor, error) {
	direction := "response"
	if d, ok := cfg["direction"].(string); ok && d != "" {
		switch d {
		case "request", "response", "both":
			direction = d
		default:
			return nil, fmt.Errorf("bytes-counted: direction must be 'request' | 'response' | 'both' (got %q)", d)
		}
	}
	granularity := uint64(1)
	if g, ok := cfg["granularity"]; ok {
		switch v := g.(type) {
		case int:
			if v <= 0 {
				return nil, fmt.Errorf("bytes-counted: granularity must be > 0")
			}
			granularity = uint64(v)
		case float64:
			if v <= 0 {
				return nil, fmt.Errorf("bytes-counted: granularity must be > 0")
			}
			granularity = uint64(v)
		default:
			return nil, fmt.Errorf("bytes-counted: granularity must be a positive number")
		}
	}
	headers := false
	if h, ok := cfg["headers"].(bool); ok {
		headers = h
	}
	return &Extractor{direction: direction, granularity: granularity, headers: headers}, nil
}

func (e *Extractor) Name() string { return Name }

func (e *Extractor) Extract(ctx context.Context, req *extractors.Request, resp *extractors.Response) (uint64, error) {
	var total uint64
	if e.direction == "request" || e.direction == "both" {
		total += uint64(len(req.Body))
		if e.headers {
			total += headerBytes(req.Headers)
		}
	}
	if e.direction == "response" || e.direction == "both" {
		total += uint64(len(resp.Body))
		if e.headers {
			total += headerBytes(resp.Headers)
		}
	}
	return total / e.granularity, nil
}

// Granularity returns the configured units-per-byte divisor.
//
// LiveCounter wiring exposes this so mode drivers (which own the running
// byte counter) can apply the same divisor at read time as Extract uses
// at end-of-session. Keeping the divisor in one place avoids drift
// between interim and final unit totals.
func (e *Extractor) Granularity() uint64 { return e.granularity }

// LiveCounter is the per-session running counter built by NewLiveCounter.
// Mode drivers increment Bytes as data flows; the payment middleware
// polls CurrentUnits every tick.
type LiveCounter struct {
	// Bytes is the running on-wire byte total for this session. Mode
	// drivers increment via atomic.AddUint64; readers Load.
	Bytes       atomic.Uint64
	granularity uint64
}

var _ extractors.LiveCounter = (*LiveCounter)(nil)

// NewLiveCounter returns a LiveCounter that converts bytes → units using
// the same granularity as the Extractor.
func (e *Extractor) NewLiveCounter() *LiveCounter {
	g := e.granularity
	if g == 0 {
		g = 1
	}
	return &LiveCounter{granularity: g}
}

// CurrentUnits returns the running unit total.
//
// Implements extractors.LiveCounter. Safe for concurrent reads while
// other goroutines call AddBytes; uses atomic.Uint64.Load.
func (lc *LiveCounter) CurrentUnits() uint64 {
	if lc == nil {
		return 0
	}
	return lc.Bytes.Load() / lc.granularity
}

// AddBytes adds n on-wire bytes to the running counter. Mode drivers
// call this from their proxy loops (e.g. ws-realtime's pumpFrames).
func (lc *LiveCounter) AddBytes(n uint64) {
	if lc == nil {
		return
	}
	lc.Bytes.Add(n)
}

// headerBytes approximates the on-wire byte cost of an http.Header set.
// Each line is "<name>: <value>\r\n" — len(name) + 2 (": ") + len(value) + 2 (\r\n).
func headerBytes(h http.Header) uint64 {
	var n uint64
	for k, vs := range h {
		for _, v := range vs {
			n += uint64(len(k)) + 4 + uint64(len(v))
		}
	}
	return n
}
