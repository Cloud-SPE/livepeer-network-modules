// Package openaiusage implements the openai-usage extractor per
// livepeer-network-protocol/extractors/openai-usage.md.
//
// Reads `usage.{prompt|completion|total}_tokens` from an OpenAI-shaped
// response body. Falls back to 0 (with a warning log) if the field is
// absent or non-numeric.
package openaiusage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
)

const Name = "openai-usage"

type Extractor struct {
	field string
}

var _ extractors.Extractor = (*Extractor)(nil)

// New is the factory function registered with the extractors registry.
func New(cfg map[string]any) (extractors.Extractor, error) {
	field := "total_tokens"
	if f, ok := cfg["field"].(string); ok && f != "" {
		switch f {
		case "prompt_tokens", "completion_tokens", "total_tokens":
			field = f
		default:
			return nil, fmt.Errorf("openai-usage: field must be prompt_tokens, completion_tokens, or total_tokens (got %q)", f)
		}
	}
	return &Extractor{field: field}, nil
}

func (e *Extractor) Name() string { return Name }

func (e *Extractor) Extract(ctx context.Context, req *extractors.Request, resp *extractors.Response) (uint64, error) {
	if len(resp.Body) == 0 {
		log.Printf("openai-usage: empty response body; using 0")
		return 0, nil
	}
	usage, ok, err := parseUsageObject(resp.Body)
	if err != nil {
		log.Printf("openai-usage: response body not JSON/SSE (%v); using 0", err)
		return 0, nil
	}
	if !ok {
		log.Printf("openai-usage: response has no 'usage' object; using 0")
		return 0, nil
	}
	v, ok := usage[e.field]
	if !ok {
		log.Printf("openai-usage: usage.%s absent; using 0", e.field)
		return 0, nil
	}
	n, err := toNonNegativeInt(v)
	if err != nil {
		log.Printf("openai-usage: usage.%s not an integer (%v); using 0", e.field, err)
		return 0, nil
	}
	return n, nil
}

func parseUsageObject(body []byte) (map[string]any, bool, error) {
	var parsed struct {
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		usage, ok := parseUsageFromSSE(body)
		if ok {
			return usage, true, nil
		}
		return nil, false, err
	}
	if parsed.Usage == nil {
		return nil, false, nil
	}
	return parsed.Usage, true, nil
}

func parseUsageFromSSE(body []byte) (map[string]any, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	var eventData []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasSuffix(line, "\r") {
			line = strings.TrimSuffix(line, "\r")
		}
		if line == "" {
			if usage, ok := parseUsageFromSSEEvent(eventData); ok {
				return usage, true
			}
			eventData = eventData[:0]
			continue
		}
		if strings.HasPrefix(line, "data:") {
			eventData = append(eventData, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if usage, ok := parseUsageFromSSEEvent(eventData); ok {
		return usage, true
	}
	return nil, false
}

func parseUsageFromSSEEvent(dataLines []string) (map[string]any, bool) {
	if len(dataLines) == 0 {
		return nil, false
	}
	payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
	if payload == "" || payload == "[DONE]" {
		return nil, false
	}
	var parsed struct {
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return nil, false
	}
	if parsed.Usage == nil {
		return nil, false
	}
	return parsed.Usage, true
}

func toNonNegativeInt(v any) (uint64, error) {
	switch n := v.(type) {
	case float64:
		if n < 0 {
			return 0, fmt.Errorf("negative")
		}
		return uint64(n), nil
	case int:
		if n < 0 {
			return 0, fmt.Errorf("negative")
		}
		return uint64(n), nil
	case int64:
		if n < 0 {
			return 0, fmt.Errorf("negative")
		}
		return uint64(n), nil
	case uint64:
		return n, nil
	default:
		return 0, fmt.Errorf("not a number: %T", v)
	}
}
