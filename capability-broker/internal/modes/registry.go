package modes

import "sort"

// Registry maps mode-name@vN strings to driver implementations.
type Registry struct {
	drivers map[string]Driver
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{drivers: map[string]Driver{}}
}

// Register adds a driver. The key is the driver's Mode() value.
func (r *Registry) Register(d Driver) {
	r.drivers[d.Mode()] = d
}

// Get returns the driver for a mode string, or false if not registered.
func (r *Registry) Get(mode string) (Driver, bool) {
	d, ok := r.drivers[mode]
	return d, ok
}

// Has returns true if mode is registered.
func (r *Registry) Has(mode string) bool {
	_, ok := r.drivers[mode]
	return ok
}

// Names returns the registered mode strings, sorted for stable diagnostics.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.drivers))
	for k := range r.drivers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
