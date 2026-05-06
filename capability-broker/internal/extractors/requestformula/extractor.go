// Package requestformula implements the request-formula extractor per
// livepeer-network-protocol/extractors/request-formula.md.
//
// Computes a non-negative integer from request fields via a safe
// arithmetic expression. The expression's grammar is enforced at
// config-load time by walking Go's parser AST and rejecting any node
// outside the allowed set:
//
//   - numeric literals (int, float)
//   - identifiers (resolved against the `fields` map)
//   - operators: + - * / %
//   - parentheses
//   - unary +/-
//   - allowed function calls: min, max, floor, ceil, round
//
// Anything else (comparisons, conditionals, attribute access, function
// calls outside the allowlist, string ops, etc.) fails at config-load
// time, never at runtime — per the spec's security-critical section.
package requestformula

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"math"
	"strconv"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

const Name = "request-formula"

type Extractor struct {
	expr         ast.Expr
	fieldPaths   map[string]string
	defaultValue uint64
}

// Compile-time interface check.
var _ extractors.Extractor = (*Extractor)(nil)

// New is the factory function registered with the extractors registry.
func New(cfg map[string]any) (extractors.Extractor, error) {
	exprStr, ok := cfg["expression"].(string)
	if !ok || exprStr == "" {
		return nil, fmt.Errorf("request-formula: expression is required")
	}
	parsed, err := parser.ParseExpr(exprStr)
	if err != nil {
		return nil, fmt.Errorf("request-formula: parse expression: %w", err)
	}
	if err := validateExpr(parsed); err != nil {
		return nil, fmt.Errorf("request-formula: invalid expression %q: %w", exprStr, err)
	}

	fieldsCfg, ok := cfg["fields"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("request-formula: fields is required (map of identifier → JSON path)")
	}
	fieldPaths := map[string]string{}
	for k, v := range fieldsCfg {
		s, ok := v.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("request-formula: fields.%s must be a non-empty string", k)
		}
		fieldPaths[k] = s
	}

	defaultValue := uint64(0)
	if d, ok := cfg["default"]; ok {
		switch v := d.(type) {
		case int:
			if v < 0 {
				return nil, fmt.Errorf("request-formula: default must be non-negative")
			}
			defaultValue = uint64(v)
		case float64:
			if v < 0 {
				return nil, fmt.Errorf("request-formula: default must be non-negative")
			}
			defaultValue = uint64(v)
		default:
			return nil, fmt.Errorf("request-formula: default must be a number")
		}
	}

	return &Extractor{expr: parsed, fieldPaths: fieldPaths, defaultValue: defaultValue}, nil
}

func (e *Extractor) Name() string { return Name }

// Extract reads request fields, evaluates the expression, returns a
// non-negative integer. On any error (missing field, non-numeric value,
// runtime eval failure), returns the configured default with a warning.
func (e *Extractor) Extract(ctx context.Context, req *extractors.Request, resp *extractors.Response) (uint64, error) {
	if len(req.Body) == 0 {
		log.Printf("request-formula: empty request body; using default %d", e.defaultValue)
		return e.defaultValue, nil
	}
	var data any
	if err := json.Unmarshal(req.Body, &data); err != nil {
		log.Printf("request-formula: request body not JSON (%v); using default %d", err, e.defaultValue)
		return e.defaultValue, nil
	}
	values := map[string]float64{}
	for ident, path := range e.fieldPaths {
		v, err := lookupAndCoerce(path, data)
		if err != nil {
			log.Printf("request-formula: field %s (%s) not numeric (%v); using default %d", ident, path, err, e.defaultValue)
			return e.defaultValue, nil
		}
		values[ident] = v
	}
	result, err := evalExpr(e.expr, values)
	if err != nil {
		log.Printf("request-formula: eval error (%v); using default %d", err, e.defaultValue)
		return e.defaultValue, nil
	}
	if result < 0 {
		return 0, nil
	}
	return uint64(math.Floor(result)), nil
}

// validateExpr walks the AST and rejects any node outside the allowed
// safe-arithmetic grammar. Called at config-load time per spec.
func validateExpr(node ast.Expr) error {
	switch n := node.(type) {
	case *ast.BasicLit:
		switch n.Kind {
		case token.INT, token.FLOAT:
			return nil
		default:
			return fmt.Errorf("unsupported literal kind: %v", n.Kind)
		}
	case *ast.Ident:
		return nil
	case *ast.BinaryExpr:
		switch n.Op {
		case token.ADD, token.SUB, token.MUL, token.QUO, token.REM:
			// allowed
		default:
			return fmt.Errorf("unsupported binary operator: %v", n.Op)
		}
		if err := validateExpr(n.X); err != nil {
			return err
		}
		return validateExpr(n.Y)
	case *ast.UnaryExpr:
		switch n.Op {
		case token.ADD, token.SUB:
			return validateExpr(n.X)
		default:
			return fmt.Errorf("unsupported unary operator: %v", n.Op)
		}
	case *ast.ParenExpr:
		return validateExpr(n.X)
	case *ast.CallExpr:
		ident, ok := n.Fun.(*ast.Ident)
		if !ok {
			return fmt.Errorf("function calls must be bare identifiers")
		}
		switch ident.Name {
		case "min", "max":
			if len(n.Args) != 2 {
				return fmt.Errorf("%s requires 2 arguments", ident.Name)
			}
		case "floor", "ceil", "round":
			if len(n.Args) != 1 {
				return fmt.Errorf("%s requires 1 argument", ident.Name)
			}
		default:
			return fmt.Errorf("unsupported function: %s", ident.Name)
		}
		for _, arg := range n.Args {
			if err := validateExpr(arg); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported expression node: %T", node)
	}
}

// evalExpr walks the AST and evaluates the expression against the field
// value map. Mirrors validateExpr's accepted grammar.
func evalExpr(node ast.Expr, values map[string]float64) (float64, error) {
	switch n := node.(type) {
	case *ast.BasicLit:
		return strconv.ParseFloat(n.Value, 64)
	case *ast.Ident:
		v, ok := values[n.Name]
		if !ok {
			return 0, fmt.Errorf("undefined identifier: %s", n.Name)
		}
		return v, nil
	case *ast.BinaryExpr:
		l, err := evalExpr(n.X, values)
		if err != nil {
			return 0, err
		}
		r, err := evalExpr(n.Y, values)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.ADD:
			return l + r, nil
		case token.SUB:
			return l - r, nil
		case token.MUL:
			return l * r, nil
		case token.QUO:
			if r == 0 {
				return 0, fmt.Errorf("divide by zero")
			}
			return l / r, nil
		case token.REM:
			if r == 0 {
				return 0, fmt.Errorf("modulo by zero")
			}
			return math.Mod(l, r), nil
		}
	case *ast.UnaryExpr:
		v, err := evalExpr(n.X, values)
		if err != nil {
			return 0, err
		}
		if n.Op == token.SUB {
			return -v, nil
		}
		return v, nil
	case *ast.ParenExpr:
		return evalExpr(n.X, values)
	case *ast.CallExpr:
		ident := n.Fun.(*ast.Ident)
		switch ident.Name {
		case "min":
			a, _ := evalExpr(n.Args[0], values)
			b, _ := evalExpr(n.Args[1], values)
			return math.Min(a, b), nil
		case "max":
			a, _ := evalExpr(n.Args[0], values)
			b, _ := evalExpr(n.Args[1], values)
			return math.Max(a, b), nil
		case "floor":
			a, _ := evalExpr(n.Args[0], values)
			return math.Floor(a), nil
		case "ceil":
			a, _ := evalExpr(n.Args[0], values)
			return math.Ceil(a), nil
		case "round":
			a, _ := evalExpr(n.Args[0], values)
			return math.Round(a), nil
		}
	}
	return 0, fmt.Errorf("unreachable: unhandled node %T", node)
}

// lookupAndCoerce evaluates a JSONPath-ish dotted path against parsed JSON
// and coerces the result to float64.
func lookupAndCoerce(path string, data any) (float64, error) {
	if !strings.HasPrefix(path, "$") {
		return 0, fmt.Errorf("path must start with $")
	}
	current := data
	rest := path[1:]
	for len(rest) > 0 {
		if rest[0] != '.' {
			return 0, fmt.Errorf("only $.foo paths supported in v0.1; got %q", path)
		}
		rest = rest[1:]
		end := strings.IndexByte(rest, '.')
		var key string
		if end == -1 {
			key = rest
			rest = ""
		} else {
			key = rest[:end]
			rest = rest[end:]
		}
		m, ok := current.(map[string]any)
		if !ok {
			return 0, fmt.Errorf("path mid-segment is not an object")
		}
		v, present := m[key]
		if !present {
			return 0, fmt.Errorf("key %q absent", key)
		}
		current = v
	}
	switch v := current.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("not numeric: %T", v)
	}
}
