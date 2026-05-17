package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// sseMaxLineBytes bounds the buffered-scanner line size. vLLM chat
// frames are typically small (a few hundred bytes), but reasoning/tool
// frames can run longer. 1 MB is more than enough headroom without
// risking unbounded memory growth on hostile inputs.
const sseMaxLineBytes = 1 << 20

// usageEnvelope captures the `usage` block on an OpenAI streaming
// chunk. vLLM (and OpenAI proper) emit this only on the FINAL chunk
// when the client sets `stream_options.include_usage: true`.
type usageEnvelope struct {
	Usage *usageFields `json:"usage,omitempty"`
}

type usageFields struct {
	PromptTokens     uint64 `json:"prompt_tokens"`
	CompletionTokens uint64 `json:"completion_tokens"`
	TotalTokens      uint64 `json:"total_tokens"`
}

// streamAndCountUsage copies the SSE response body from src to dst,
// flushing each frame, while scanning `data:` lines for a final
// `usage` object. It returns the field selected by usageField
// ("total_tokens" / "prompt_tokens" / "completion_tokens") on the last
// usage-bearing frame, or 0 if none was seen.
//
// The function is resilient to:
//   - `data: [DONE]` sentinels
//   - Frames split across read boundaries (bufio.Scanner handles it)
//   - Frames without a usage block (intermediate token chunks)
//   - Malformed JSON in individual frames (logged and skipped, count
//     stays at the last good value)
func streamAndCountUsage(dst io.Writer, src io.Reader, usageField string, flush func()) uint64 {
	var lastTotal uint64
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), sseMaxLineBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		// Always forward the raw line + newline to the client.
		// Preserving byte-for-byte SSE shape matters because clients
		// may have their own framing assumptions.
		_, _ = dst.Write(line)
		_, _ = dst.Write([]byte("\n"))
		if flush != nil {
			flush()
		}
		if frame, ok := parseDataFrame(line); ok {
			if n, present := extractUsageField(frame, usageField); present {
				lastTotal = n
			}
		}
	}
	// Scanner errors (oversized line, transport closed) end the loop
	// but don't fail the request — clients have already received the
	// bytes scanned so far. The trailer reflects whatever usage frame
	// we saw, defaulting to 0.
	return lastTotal
}

// parseDataFrame returns the JSON payload of a `data: ...` SSE line,
// or (nil, false) for blank lines, comments, the `[DONE]` sentinel,
// or non-data event lines (`event:`, `id:`, etc.).
func parseDataFrame(line []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return nil, false
	}
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return nil, false
	}
	payload := bytes.TrimSpace(trimmed[len("data:"):])
	if len(payload) == 0 {
		return nil, false
	}
	if bytes.Equal(payload, []byte("[DONE]")) {
		return nil, false
	}
	return payload, true
}

// extractUsageField parses a single SSE data frame and returns the
// requested usage field if the frame includes a `usage` object.
// Frames without `usage` (typical of intermediate token chunks) return
// (0, false); the caller keeps the previously seen value.
func extractUsageField(payload []byte, field string) (uint64, bool) {
	var env usageEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return 0, false
	}
	if env.Usage == nil {
		return 0, false
	}
	switch field {
	case "prompt_tokens":
		return env.Usage.PromptTokens, true
	case "completion_tokens":
		return env.Usage.CompletionTokens, true
	default:
		return env.Usage.TotalTokens, true
	}
}

// ensureIncludeUsage edits a streaming chat-completions request body so
// vLLM emits a final usage frame. The shape of the request body is
// preserved (unknown keys round-trip). Returns the (possibly rewritten)
// body, a flag indicating whether the request is actually streaming,
// and an error if the body is not valid JSON.
//
// Behaviour:
//   - If `stream` is absent or false → returns body unchanged, isStream=false.
//   - If `stream` is true and `stream_options.include_usage` is already
//     present → returns body unchanged, isStream=true.
//   - If `stream` is true and the flag is absent → injects
//     `stream_options.include_usage: true`, leaves any sibling
//     stream_options fields intact, returns the rewritten body.
func ensureIncludeUsage(body []byte) ([]byte, bool, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return body, false, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, false, err
	}
	streaming, _ := doc["stream"].(bool)
	if !streaming {
		return body, false, nil
	}
	opts, ok := doc["stream_options"].(map[string]any)
	if !ok {
		opts = map[string]any{}
	}
	if v, present := opts["include_usage"]; present {
		// Honour whatever the client set, even if false. We only
		// inject when absent.
		if b, ok := v.(bool); ok && b {
			return body, true, nil
		}
		return body, true, nil
	}
	opts["include_usage"] = true
	doc["stream_options"] = opts
	rewritten, err := json.Marshal(doc)
	if err != nil {
		return body, true, err
	}
	return rewritten, true, nil
}

// errResponseNotSSE is returned by guardSSEContentType when the
// upstream response is not a server-sent-events stream. Callers fall
// back to a transparent body copy in that case.
var errResponseNotSSE = errors.New("upstream response is not text/event-stream")

// guardSSEContentType checks that the upstream response advertises an
// SSE content type. vLLM uses `text/event-stream`. Some configurations
// emit `text/event-stream; charset=utf-8`; we accept any prefix match.
func guardSSEContentType(h http.Header) error {
	ct := h.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(ct), "text/event-stream") {
		return errResponseNotSSE
	}
	return nil
}
