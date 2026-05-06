package extractors

import (
	"fmt"
	"sort"
)

// Registry maps extractor type names to factories. The broker builds one
// extractor instance per request via Build (or per startup, for caching).
type Registry struct {
	factories map[string]Factory
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

// Register associates a factory with a type name (e.g., "response-jsonpath").
// Calling Register with a duplicate name overwrites the previous factory.
func (r *Registry) Register(name string, f Factory) {
	r.factories[name] = f
}

// Build instantiates an extractor from a config map. The map MUST contain a
// "type" string field naming a registered factory.
func (r *Registry) Build(cfg map[string]any) (Extractor, error) {
	t, ok := cfg["type"].(string)
	if !ok {
		return nil, fmt.Errorf("extractor: type field is required and must be a string")
	}
	f, ok := r.factories[t]
	if !ok {
		return nil, fmt.Errorf("extractor: type %q is not registered (available: %v)", t, r.Names())
	}
	return f(cfg)
}

// Has returns true if the named extractor type is registered.
func (r *Registry) Has(name string) bool {
	_, ok := r.factories[name]
	return ok
}

// Names returns the registered extractor type names, sorted for stable
// diagnostics.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.factories))
	for k := range r.factories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
