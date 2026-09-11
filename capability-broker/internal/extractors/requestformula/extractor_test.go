package requestformula

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
)

// TTS is priced per character, and "character" means Unicode code point
// — the only count that is the same number for the same text however it
// was encoded. A byte count would charge twice as much for Greek as for
// English and three times for most CJK.
func TestTextFieldsCountsCodePoints(t *testing.T) {
	ex, err := New(map[string]any{
		"expression":  "chars",
		"text_fields": map[string]any{"chars": "$.input"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   string
		want uint64
	}{
		{"ascii", "hello", 5},
		{"greek", "γειά σου", 8},
		{"cjk", "你好世界", 4},
		{"emoji-bmp-mix", "hi ☕", 4},
		{"empty", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"input": tc.in})
			if err != nil {
				t.Fatal(err)
			}
			got, err := ex.Extract(context.Background(),
				&extractors.Request{Body: body}, &extractors.Response{Status: 200})
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("%q counted %d code points; want %d (bytes would be %d)",
					tc.in, got, tc.want, len(tc.in))
			}
		})
	}
}

// A numeric field named as text, or the reverse, falls to the default
// rather than billing something incidental — a length is not a quantity
// just because both are numbers.
func TestTextFieldsRefusesNonString(t *testing.T) {
	ex, err := New(map[string]any{
		"expression":  "chars",
		"text_fields": map[string]any{"chars": "$.input"},
		"default":     0,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"input": 42}`)
	got, err := ex.Extract(context.Background(),
		&extractors.Request{Body: body}, &extractors.Response{Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("numeric value in a text field billed %d; want the default 0", got)
	}
}

// The same identifier cannot mean two things.
func TestTextFieldsRejectsDuplicateIdentifier(t *testing.T) {
	_, err := New(map[string]any{
		"expression":  "x",
		"fields":      map[string]any{"x": "$.a"},
		"text_fields": map[string]any{"x": "$.b"},
	})
	if err == nil {
		t.Fatal("expected an error for an identifier declared in both fields and text_fields")
	}
}

// text_fields alone is a complete configuration — TTS needs no numeric
// field at all.
func TestTextFieldsAloneIsValid(t *testing.T) {
	if _, err := New(map[string]any{
		"expression":  "chars",
		"text_fields": map[string]any{"chars": "$.input"},
	}); err != nil {
		t.Fatalf("text_fields alone rejected: %v", err)
	}
}
