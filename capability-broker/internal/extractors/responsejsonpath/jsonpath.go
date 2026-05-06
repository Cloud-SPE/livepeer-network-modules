package responsejsonpath

import (
	"fmt"
	"strconv"
	"strings"
)

// eval evaluates a JSONPath expression against parsed JSON, returning the
// matching value(s). Implements the spec's required minimum subset:
//
//	$           — root
//	.<key>      — child by name
//	[<idx>]     — array index
//	[<n1>,...]  — array index list (returns all matched values)
//
// Anything outside this subset returns an error from eval.
func eval(path string, data any) ([]any, error) {
	if !strings.HasPrefix(path, "$") {
		return nil, fmt.Errorf("path must start with $ (got %q)", path)
	}
	current := []any{data}
	rest := path[1:]
	for len(rest) > 0 {
		switch rest[0] {
		case '.':
			rest = rest[1:]
			end := strings.IndexAny(rest, ".[")
			var key string
			if end == -1 {
				key = rest
				rest = ""
			} else {
				key = rest[:end]
				rest = rest[end:]
			}
			if key == "" {
				return nil, fmt.Errorf("empty key after '.' in path")
			}
			next := make([]any, 0, len(current))
			for _, v := range current {
				m, ok := v.(map[string]any)
				if !ok {
					continue
				}
				if val, present := m[key]; present {
					next = append(next, val)
				}
			}
			current = next
		case '[':
			end := strings.Index(rest, "]")
			if end == -1 {
				return nil, fmt.Errorf("unterminated [...] in path")
			}
			indexExpr := rest[1:end]
			rest = rest[end+1:]
			indices := []int{}
			for _, p := range strings.Split(indexExpr, ",") {
				i, err := strconv.Atoi(strings.TrimSpace(p))
				if err != nil {
					return nil, fmt.Errorf("invalid index %q in path: %w", p, err)
				}
				indices = append(indices, i)
			}
			next := make([]any, 0, len(current)*len(indices))
			for _, v := range current {
				arr, ok := v.([]any)
				if !ok {
					continue
				}
				for _, i := range indices {
					if i >= 0 && i < len(arr) {
						next = append(next, arr[i])
					}
				}
			}
			current = next
		default:
			return nil, fmt.Errorf("unexpected character %q in path", rest[0])
		}
	}
	return current, nil
}
