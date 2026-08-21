package audioduration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"mime/multipart"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
)

// Name is the extractor type id declared in host-config.yaml.
const Name = "multipart-audio-duration"

// Extractor bills a transcription request by the playing time of the
// audio it uploaded.
type Extractor struct {
	fileField    string
	unit         string
	allowInexact bool
	defaultValue uint64
	maxSeconds   float64
}

var _ extractors.Extractor = (*Extractor)(nil)

// New builds the extractor.
//
//	type: multipart-audio-duration
//	file_field: file        # form field holding the audio (default "file")
//	unit: seconds           # or "milliseconds" (default "seconds")
//	allow_inexact: false    # bill a CBR-estimated MP3 duration
//	default: 0              # units when the duration cannot be measured
//	max_seconds: 0          # refuse (bill default) beyond this; 0 = no cap
func New(cfg map[string]any) (extractors.Extractor, error) {
	e := &Extractor{fileField: "file", unit: "seconds"}
	if v, ok := cfg["file_field"]; ok {
		s, ok := v.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("%s: file_field must be a non-empty string", Name)
		}
		e.fileField = s
	}
	if v, ok := cfg["unit"]; ok {
		s, _ := v.(string)
		switch s {
		case "seconds", "milliseconds":
			e.unit = s
		default:
			return nil, fmt.Errorf("%s: unit must be \"seconds\" or \"milliseconds\", got %q", Name, s)
		}
	}
	if v, ok := cfg["allow_inexact"]; ok {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("%s: allow_inexact must be a bool", Name)
		}
		e.allowInexact = b
	}
	if v, ok := cfg["default"]; ok {
		n, err := toUint(v)
		if err != nil {
			return nil, fmt.Errorf("%s: default: %w", Name, err)
		}
		e.defaultValue = n
	}
	if v, ok := cfg["max_seconds"]; ok {
		n, err := toUint(v)
		if err != nil {
			return nil, fmt.Errorf("%s: max_seconds: %w", Name, err)
		}
		e.maxSeconds = float64(n)
	}
	return e, nil
}

func toUint(v any) (uint64, error) {
	switch n := v.(type) {
	case int:
		if n < 0 {
			return 0, fmt.Errorf("must be non-negative")
		}
		return uint64(n), nil
	case int64:
		if n < 0 {
			return 0, fmt.Errorf("must be non-negative")
		}
		return uint64(n), nil
	case float64:
		if n < 0 {
			return 0, fmt.Errorf("must be non-negative")
		}
		return uint64(n), nil
	default:
		return 0, fmt.Errorf("must be a number, got %T", v)
	}
}

func (e *Extractor) Name() string { return Name }

// Extract measures the uploaded audio.
//
// Every failure path bills the configured default rather than guessing.
// The alternative — falling back to some other signal, or to the
// caller's own claim — bills for something nobody measured, and it would
// do so silently on exactly the inputs the parser got wrong.
func (e *Extractor) Extract(_ context.Context, req *extractors.Request, _ *extractors.Response) (uint64, error) {
	audio, err := e.audioFrom(req)
	if err != nil {
		log.Printf("%s: %v; billing default %d", Name, err, e.defaultValue)
		return e.defaultValue, nil
	}
	res, err := Probe(audio)
	if err != nil {
		log.Printf("%s: %v; billing default %d", Name, err, e.defaultValue)
		return e.defaultValue, nil
	}
	if !res.Exact && !e.allowInexact {
		// A CBR estimate on a VBR file can be far out. An offering that
		// would rather under-bill than over-bill a customer says so by
		// leaving allow_inexact off, which is the default.
		log.Printf("%s: %s duration is an estimate and allow_inexact is off; billing default %d",
			Name, res.Format, e.defaultValue)
		return e.defaultValue, nil
	}
	secs := res.Duration.Seconds()
	if e.maxSeconds > 0 && secs > e.maxSeconds {
		log.Printf("%s: measured %.0fs exceeds max_seconds %.0f; billing default %d",
			Name, secs, e.maxSeconds, e.defaultValue)
		return e.defaultValue, nil
	}
	if e.unit == "milliseconds" {
		return uint64(math.Ceil(float64(res.Duration.Nanoseconds()) / 1e6)), nil
	}
	// Ceiling: a 0.4-second clip is one billable second, and a rounding
	// rule that can return 0 for delivered work bills nothing for it.
	return uint64(math.Ceil(secs)), nil
}

// audioFrom pulls the audio part out of the request body.
//
// A non-multipart body is treated as the audio itself, so the same
// extractor serves an offering whose runner takes a raw upload.
func (e *Extractor) audioFrom(req *extractors.Request) ([]byte, error) {
	if req == nil || len(req.Body) == 0 {
		return nil, fmt.Errorf("empty request body")
	}
	ct := ""
	if req.Headers != nil {
		ct = req.Headers.Get("Content-Type")
	}
	if !strings.HasPrefix(strings.ToLower(ct), "multipart/") {
		return req.Body, nil
	}
	_, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return nil, fmt.Errorf("content-type: %w", err)
	}
	boundary, ok := params["boundary"]
	if !ok {
		return nil, fmt.Errorf("multipart content-type has no boundary")
	}
	mr := multipart.NewReader(bytes.NewReader(req.Body), boundary)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return nil, fmt.Errorf("no %q part in the upload", e.fileField)
		}
		if err != nil {
			return nil, fmt.Errorf("multipart: %w", err)
		}
		if part.FormName() != e.fileField {
			_ = part.Close()
			continue
		}
		b, err := io.ReadAll(part)
		_ = part.Close()
		if err != nil {
			return nil, fmt.Errorf("reading %q: %w", e.fileField, err)
		}
		return b, nil
	}
}
