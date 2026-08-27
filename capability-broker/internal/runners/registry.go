// Package runners is the broker's registry of attached runners: every
// host that has sent an accepted attach document over a live connection
// (protocols/runner-attach.md §2), what it declared, and the connection
// to dispatch to. It is the source for GET /admin/v1/runners
// (broker-admin §3) and, once offers match and freeze (plan 0043 item 8),
// for dispatch.
//
// Per §2: one connection carries one document; the document is a full
// replacement on re-send; a host's capabilities are the union across its
// connections; nothing survives a disconnect except the disconnected
// record, kept for retention so an operator can still see why a host
// went away.
package runners

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
)

// Conn is what a connection must offer: dispatch and close.
type Conn interface {
	backend.Forwarder
	io.Closer
}

// Enrollment is what the credential store said about the host.
type Enrollment struct {
	CredentialID     string `json:"credential_id"`
	Label            string `json:"label,omitempty"`
	MemberEthAddress string `json:"member_eth_address,omitempty"`
}

type connection struct {
	id      string
	conn    Conn
	doc     *runnerattach.Document
	result  *runnerattach.Result
	since   time.Time
	updated time.Time
}

// Host is one attached (or recently detached) runner.
type Host struct {
	HostID     string
	Enrollment Enrollment
	conns      map[string]*connection
	// Detached is set when the last connection went away.
	Detached time.Time
	Since    time.Time
	LastSeen time.Time
}

// CapabilityView is one accepted or rejected capability as the admin API
// reports it, with the connection it lives on.
type CapabilityView struct {
	ConnID     string
	Capability *runnerattach.Capability // nil when rejected
	Result     runnerattach.CapabilityResult
}

// Snapshot is a read-only copy of a host for reporting and matching.
type Snapshot struct {
	HostID          string
	Enrollment      Enrollment
	State           string // connected | disconnected
	Since           time.Time
	LastSeen        time.Time
	Connections     int
	AgentVersion    string
	ContractVersion string
	Hardware        []runnerattach.Hardware
	Capabilities    []CapabilityView
	Extensions      map[string]interface{}
}

// Registry holds every host.
type Registry struct {
	mu        sync.RWMutex
	hosts     map[string]*Host
	retention time.Duration
	now       func() time.Time
	// OnChange, when set, is called after any attach/detach with the
	// affected host id — the offer engine re-matches on it.
	OnChange func(hostID string)
}

// New constructs a registry keeping disconnected hosts for retention
// (default 24 h).
func New(retention time.Duration) *Registry {
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	return &Registry{hosts: map[string]*Host{}, retention: retention, now: func() time.Time { return time.Now().UTC() }}
}

// Attach records an accepted document on a connection. Re-sending on
// the same connId replaces the previous document.
func (r *Registry) Attach(connID string, conn Conn, enr Enrollment, doc *runnerattach.Document, res *runnerattach.Result) {
	now := r.now()
	r.mu.Lock()
	h := r.hosts[doc.HostID]
	if h == nil {
		h = &Host{HostID: doc.HostID, conns: map[string]*connection{}, Since: now}
		r.hosts[doc.HostID] = h
	}
	if len(h.conns) == 0 {
		h.Since = now
	}
	h.Detached = time.Time{}
	h.Enrollment = enr
	h.LastSeen = now
	c := h.conns[connID]
	if c == nil {
		c = &connection{id: connID, conn: conn, since: now}
		h.conns[connID] = c
	}
	c.doc, c.result, c.updated = doc, res, now
	r.mu.Unlock()
	if r.OnChange != nil {
		r.OnChange(doc.HostID)
	}
}

// Detach removes a connection; the host becomes disconnected when its
// last connection goes.
func (r *Registry) Detach(hostID, connID string) {
	now := r.now()
	r.mu.Lock()
	h := r.hosts[hostID]
	if h != nil {
		delete(h.conns, connID)
		if len(h.conns) == 0 {
			h.Detached = now
		}
		h.LastSeen = now
	}
	r.mu.Unlock()
	if h != nil && r.OnChange != nil {
		r.OnChange(hostID)
	}
}

// Touch updates LastSeen (keepalive / successful dispatch).
func (r *Registry) Touch(hostID string) {
	r.mu.Lock()
	if h := r.hosts[hostID]; h != nil {
		h.LastSeen = r.now()
	}
	r.mu.Unlock()
}

// Disconnect closes every connection of a host; returns how many.
func (r *Registry) Disconnect(hostID string) int {
	r.mu.RLock()
	h := r.hosts[hostID]
	var conns []Conn
	if h != nil {
		for _, c := range h.conns {
			conns = append(conns, c.conn)
		}
	}
	r.mu.RUnlock()
	for _, c := range conns {
		_ = c.Close()
	}
	return len(conns)
}

// Evict drops disconnected hosts past retention.
func (r *Registry) Evict() int {
	cutoff := r.now().Add(-r.retention)
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for id, h := range r.hosts {
		if len(h.conns) == 0 && !h.Detached.IsZero() && h.Detached.Before(cutoff) {
			delete(r.hosts, id)
			n++
		}
	}
	return n
}

// Get snapshots one host.
func (r *Registry) Get(hostID string) (Snapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h := r.hosts[hostID]
	if h == nil {
		return Snapshot{}, false
	}
	return snapshot(h), true
}

// List snapshots every host, sorted by host id.
func (r *Registry) List() []Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Snapshot, 0, len(r.hosts))
	for _, h := range r.hosts {
		out = append(out, snapshot(h))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HostID < out[j].HostID })
	return out
}

// ConnFor returns the connection carrying a given local_id on a host,
// for dispatch (runner-attach §7).
func (r *Registry) ConnFor(hostID, localID string) (Conn, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h := r.hosts[hostID]
	if h == nil {
		return nil, false
	}
	for _, c := range h.conns {
		if c.doc == nil {
			continue
		}
		for i := range c.doc.Capabilities {
			if c.doc.Capabilities[i].LocalID == localID {
				return c.conn, true
			}
		}
	}
	return nil, false
}

func snapshot(h *Host) Snapshot {
	s := Snapshot{HostID: h.HostID, Enrollment: h.Enrollment, Since: h.Since, LastSeen: h.LastSeen, Connections: len(h.conns)}
	if len(h.conns) == 0 {
		s.State = "disconnected"
	} else {
		s.State = "connected"
	}
	// Deterministic order across connections.
	ids := make([]string, 0, len(h.conns))
	for id := range h.conns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	seenGPU := map[string]bool{}
	for _, id := range ids {
		c := h.conns[id]
		if c.doc == nil {
			continue
		}
		s.AgentVersion, s.ContractVersion = c.doc.AgentVersion, c.doc.ContractVersion
		if s.Extensions == nil && len(c.doc.Extensions) > 0 {
			s.Extensions = map[string]interface{}{}
			for k, v := range c.doc.Extensions {
				s.Extensions[k] = v
			}
		}
		for _, hw := range c.doc.Hardware {
			if !seenGPU[hw.GPUUUID] {
				seenGPU[hw.GPUUUID] = true
				s.Hardware = append(s.Hardware, hw)
			}
		}
		accepted := map[int]*runnerattach.Capability{}
		for i := range c.doc.Capabilities {
			accepted[c.doc.Capabilities[i].Index] = &c.doc.Capabilities[i]
		}
		if c.result != nil {
			for _, cr := range c.result.Capabilities {
				s.Capabilities = append(s.Capabilities, CapabilityView{ConnID: id, Capability: accepted[cr.Index], Result: cr})
			}
		}
	}
	return s
}

// --- dispatch ---------------------------------------------------------------

// VirtualScheme addresses an attached runner as a backend URL:
//
//	runner://<host_id>/<local_id><path>
//
// The paid path already dials backends through a forwarder, so making
// an attached runner reachable this way means dispatch does not have to
// learn a second way to reach a backend (plan 0043 item 10).
const VirtualScheme = "runner"

// LocalIDHeader is the routing key the agent maps back to a container
// (runner-attach §7).
const LocalIDHeader = "Livepeer-Runner-Local-Id"

// BackendURL builds the virtual backend URL for one eligible pair.
func BackendURL(hostID, localID string) string {
	return VirtualScheme + "://" + url.PathEscape(hostID) + "/" + url.PathEscape(localID)
}

// ErrNotConnected reports a pair whose connection is gone. Selection is
// a snapshot, so a runner can disappear between choosing and dialing.
var ErrNotConnected = errors.New("runners: no attach connection for the selected runner")

// Forward implements backend.Forwarder for runner:// URLs: it resolves
// the host and local id, sets the routing header, and hands the request
// to that runner's attach connection.
func (r *Registry) Forward(ctx context.Context, req backend.ForwardRequest) (*http.Response, error) {
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, err
	}
	hostID, err := url.PathUnescape(u.Host)
	if err != nil {
		return nil, err
	}
	localID, rest := splitLocalID(u.Path)
	conn, ok := r.ConnFor(hostID, localID)
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", ErrNotConnected, hostID, localID)
	}
	next := req
	// The runner's own paths are relative; the agent joins them onto the
	// container's base URL, so what travels is the path and nothing else.
	next.URL = "http://worker.local" + rest
	if next.Headers == nil {
		next.Headers = make(http.Header)
	} else {
		next.Headers = next.Headers.Clone()
	}
	next.Headers.Set(LocalIDHeader, localID)
	resp, err := conn.Forward(ctx, next)
	if err == nil {
		r.Touch(hostID)
	}
	return resp, err
}

// splitLocalID peels the local id off the front of the path and returns
// it with the remainder (always rooted).
func splitLocalID(path string) (localID, rest string) {
	trimmed := strings.TrimPrefix(path, "/")
	idPart, remainder, found := strings.Cut(trimmed, "/")
	id, err := url.PathUnescape(idPart)
	if err != nil {
		id = idPart
	}
	if !found || remainder == "" {
		return id, "/"
	}
	return id, "/" + remainder
}

var _ backend.Forwarder = (*Registry)(nil)
