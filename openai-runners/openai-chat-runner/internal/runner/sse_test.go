package runner

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const vllmStreamFixture = `data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}

data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"}}]}

data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"}}]}

data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{}, "finish_reason":"stop"}]}

data: {"id":"x","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":30,"total_tokens":42}}

data: [DONE]

`

func TestStreamAndCountUsage_ExtractsFinalTotalTokens(t *testing.T) {
	var out bytes.Buffer
	got := streamAndCountUsage(&out, strings.NewReader(vllmStreamFixture), "total_tokens", nil)
	if got != 42 {
		t.Fatalf("total_tokens = %d; want 42", got)
	}
	// Body must be forwarded byte-equivalent (line content preserved).
	if !bytes.Contains(out.Bytes(), []byte(`"content":"Hello"`)) {
		t.Fatal("forwarded body missing assistant token")
	}
	if !bytes.Contains(out.Bytes(), []byte("[DONE]")) {
		t.Fatal("forwarded body missing DONE sentinel")
	}
}

func TestStreamAndCountUsage_HonoursFieldSelector(t *testing.T) {
	cases := map[string]uint64{
		"total_tokens":      42,
		"prompt_tokens":     12,
		"completion_tokens": 30,
	}
	for field, want := range cases {
		var out bytes.Buffer
		got := streamAndCountUsage(&out, strings.NewReader(vllmStreamFixture), field, nil)
		if got != want {
			t.Errorf("field=%s: got %d, want %d", field, got, want)
		}
	}
}

func TestStreamAndCountUsage_NoUsageFrameReturnsZero(t *testing.T) {
	// Older Ollama (pre-v0.5) silently drops stream_options.include_usage
	// — the final SSE frame has no `usage` object. The runner must not
	// crash; it bills 0 and the body still forwards intact. Operator is
	// expected to upgrade Ollama; this test guards against regression.
	noUsage := strings.Replace(
		vllmStreamFixture,
		`data: {"id":"x","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":30,"total_tokens":42}}`+"\n\n",
		"",
		1,
	)
	var out bytes.Buffer
	got := streamAndCountUsage(&out, strings.NewReader(noUsage), "total_tokens", nil)
	if got != 0 {
		t.Fatalf("expected 0 when no usage frame is present; got %d", got)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"content":"Hello"`)) {
		t.Fatal("body should still stream through even without a usage frame")
	}
}

func TestStreamAndCountUsage_IgnoresMalformedFrame(t *testing.T) {
	stream := "data: not-json\n\n" +
		`data: {"choices":[],"usage":{"total_tokens":7}}` + "\n\n" +
		"data: [DONE]\n\n"
	var out bytes.Buffer
	got := streamAndCountUsage(&out, strings.NewReader(stream), "total_tokens", nil)
	if got != 7 {
		t.Fatalf("malformed frame should be skipped; got %d, want 7", got)
	}
}

func TestParseDataFrame(t *testing.T) {
	cases := map[string]struct {
		in   string
		ok   bool
		want string
	}{
		"data-json":   {`data: {"a":1}`, true, `{"a":1}`},
		"blank":       {"", false, ""},
		"comment":     {": keep-alive", false, ""},
		"event":       {"event: ping", false, ""},
		"done":        {"data: [DONE]", false, ""},
		"empty-data":  {"data: ", false, ""},
		"with-spaces": {"   data:   {\"b\":2}   ", true, `{"b":2}`},
	}
	for name, c := range cases {
		got, ok := parseDataFrame([]byte(c.in))
		if ok != c.ok {
			t.Errorf("%s: ok=%v want %v", name, ok, c.ok)
			continue
		}
		if ok && string(got) != c.want {
			t.Errorf("%s: got %q want %q", name, got, c.want)
		}
	}
}

func TestEnsureIncludeUsage_NonStreamingPassThrough(t *testing.T) {
	in := []byte(`{"model":"x","messages":[{"role":"user","content":"hi"}]}`)
	out, isStream, err := ensureIncludeUsage(in)
	if err != nil {
		t.Fatal(err)
	}
	if isStream {
		t.Fatal("expected isStream=false")
	}
	if !bytes.Equal(in, out) {
		t.Fatalf("body should be unchanged; got %s", out)
	}
}

func TestEnsureIncludeUsage_StreamingInjects(t *testing.T) {
	in := []byte(`{"model":"x","stream":true}`)
	out, isStream, err := ensureIncludeUsage(in)
	if err != nil {
		t.Fatal(err)
	}
	if !isStream {
		t.Fatal("expected isStream=true")
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	opts, ok := got["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("stream_options missing: %s", out)
	}
	if v, _ := opts["include_usage"].(bool); !v {
		t.Fatalf("include_usage should be true; got %v", opts["include_usage"])
	}
}

func TestEnsureIncludeUsage_PreservesExistingStreamOptions(t *testing.T) {
	in := []byte(`{"model":"x","stream":true,"stream_options":{"another_flag":"keep"}}`)
	out, _, err := ensureIncludeUsage(in)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	opts := got["stream_options"].(map[string]any)
	if opts["another_flag"] != "keep" {
		t.Fatalf("sibling stream_options field lost: %v", opts)
	}
	if v, _ := opts["include_usage"].(bool); !v {
		t.Fatalf("include_usage should be injected; got %v", opts["include_usage"])
	}
}

func TestEnsureIncludeUsage_HonoursExistingIncludeUsage(t *testing.T) {
	// If the client already set include_usage (even to false), don't override.
	in := []byte(`{"model":"x","stream":true,"stream_options":{"include_usage":false}}`)
	out, _, err := ensureIncludeUsage(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(in, out) {
		t.Fatalf("body should be unchanged when include_usage is explicitly set; got %s", out)
	}
}

func TestEnsureIncludeUsage_RejectsInvalidJSON(t *testing.T) {
	_, _, err := ensureIncludeUsage([]byte("not json"))
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
}
