package audioduration

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
)

func multipartBody(t *testing.T, field, filename string, content []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("model", "whisper-1"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), w.FormDataContentType()
}

func extract(t *testing.T, e extractors.Extractor, body []byte, ct string) uint64 {
	t.Helper()
	h := http.Header{}
	if ct != "" {
		h.Set("Content-Type", ct)
	}
	n, err := e.Extract(context.Background(), &extractors.Request{Body: body, Headers: h},
		&extractors.Response{Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestExtractMultipartWAVSeconds(t *testing.T) {
	e, err := New(map[string]any{"type": Name})
	if err != nil {
		t.Fatal(err)
	}
	body, ct := multipartBody(t, "file", "speech.wav", wavFixture(16000, 1, 16, 48000))
	if got := extract(t, e, body, ct); got != 3 {
		t.Fatalf("billed %d units; want 3 seconds", got)
	}
}

// Ceiling, not truncation: a rounding rule that returns 0 for delivered
// work bills nothing for it.
func TestExtractRoundsUpPartialSecond(t *testing.T) {
	e, err := New(map[string]any{"type": Name})
	if err != nil {
		t.Fatal(err)
	}
	// 16 kHz mono, 6400 frames = 0.4s.
	body, ct := multipartBody(t, "file", "short.wav", wavFixture(16000, 1, 16, 6400))
	if got := extract(t, e, body, ct); got != 1 {
		t.Fatalf("billed %d for a 0.4s clip; want 1", got)
	}
}

func TestExtractMilliseconds(t *testing.T) {
	e, err := New(map[string]any{"type": Name, "unit": "milliseconds"})
	if err != nil {
		t.Fatal(err)
	}
	body, ct := multipartBody(t, "file", "speech.wav", wavFixture(16000, 1, 16, 8000))
	if got := extract(t, e, body, ct); got != 500 {
		t.Fatalf("billed %d ms; want 500", got)
	}
}

// A raw upload with no multipart wrapper is the audio itself, so one
// extractor serves both request shapes.
func TestExtractRawBody(t *testing.T) {
	e, err := New(map[string]any{"type": Name})
	if err != nil {
		t.Fatal(err)
	}
	if got := extract(t, e, wavFixture(16000, 1, 16, 32000), "audio/wav"); got != 2 {
		t.Fatalf("billed %d; want 2 seconds from a raw body", got)
	}
}

func TestExtractHonoursFileField(t *testing.T) {
	e, err := New(map[string]any{"type": Name, "file_field": "audio"})
	if err != nil {
		t.Fatal(err)
	}
	body, ct := multipartBody(t, "audio", "speech.wav", wavFixture(16000, 1, 16, 16000))
	if got := extract(t, e, body, ct); got != 1 {
		t.Fatalf("billed %d; want 1", got)
	}
}

// Every failure path bills the configured default. Falling back to some
// other signal would bill for something nobody measured, silently, on
// exactly the inputs the parser got wrong.
func TestExtractUnmeasurableBillsDefault(t *testing.T) {
	e, err := New(map[string]any{"type": Name, "default": 0})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		body []byte
		ct   string
	}{
		{"not audio", []byte("plain text, no container"), "audio/wav"},
		{"empty", nil, "audio/wav"},
		{"multipart without the file part", func() []byte {
			b, _ := multipartBody(t, "other", "x.wav", wavFixture(16000, 1, 16, 16000))
			return b
		}(), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct := tc.ct
			if ct == "" {
				var buf bytes.Buffer
				w := multipart.NewWriter(&buf)
				ct = w.FormDataContentType()
			}
			if got := extract(t, e, tc.body, ct); got != 0 {
				t.Fatalf("billed %d for an unmeasurable upload; want the default 0", got)
			}
		})
	}
}

// max_seconds is a ceiling on what one exchange can bill, so a misparse
// cannot turn into a large charge.
func TestExtractMaxSeconds(t *testing.T) {
	e, err := New(map[string]any{"type": Name, "max_seconds": 2, "default": 0})
	if err != nil {
		t.Fatal(err)
	}
	body, ct := multipartBody(t, "file", "long.wav", wavFixture(16000, 1, 16, 16000*10))
	if got := extract(t, e, body, ct); got != 0 {
		t.Fatalf("billed %d past max_seconds; want the default 0", got)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	for _, cfg := range []map[string]any{
		{"type": Name, "unit": "furlongs"},
		{"type": Name, "file_field": ""},
		{"type": Name, "default": -1},
		{"type": Name, "allow_inexact": "yes"},
	} {
		if _, err := New(cfg); err == nil {
			t.Fatalf("expected an error for %v", cfg)
		}
	}
}
