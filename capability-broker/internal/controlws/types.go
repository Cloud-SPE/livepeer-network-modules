// Package controlws defines the shared control-WS envelope and lifecycle
// frame vocabulary used by every interaction-mode driver that runs a
// long-lived control WebSocket (currently session-control-plus-media@v0
// and session-control-external-media@v0).
//
// The intent is a single source of truth for the lifecycle frame names
// across modes, so a frame parser written against this package's
// constants accepts identical wire types from either mode. Per-mode
// drivers continue to own their own WS handlers (upgrade, read pump,
// write pump, heartbeat) because the per-mode session-store types,
// backend hooks, and frame vocabularies differ enough that a single
// shared handler would obscure more than it would share.
package controlws

import (
	"encoding/json"
	"errors"
)

// Envelope is the small fixed wrapper a broker parses on every control
// WebSocket text frame. `Body` is opaque on the workload axis — modes
// that forward frames to a backend pass it verbatim; modes that own
// terminal frame semantics interpret it themselves.
type Envelope struct {
	Type string          `json:"type"`
	Seq  uint64          `json:"seq,omitempty"`
	Body json.RawMessage `json:"body,omitempty"`
}

// Reserved lifecycle envelope types — short-circuited by every long-lived
// control-WS mode. Modes that need additional types (e.g. media-plane
// signaling for session-control-plus-media@v0) define those constants
// in their own package.
const (
	TypeSessionStarted     = "session.started"
	TypeSessionEnd         = "session.end"
	TypeSessionEnded       = "session.ended"
	TypeSessionError       = "session.error"
	TypeSessionUsageTick   = "session.usage.tick"
	TypeSessionBalanceLow  = "session.balance.low"
	TypeSessionBalanceRefilled = "session.balance.refilled"
	TypeSessionReconnected = "session.reconnected"
	TypeSessionTopup       = "session.topup"
)

// IsLifecycle reports whether the envelope type is one of the
// reserved lifecycle frames defined above.
func IsLifecycle(t string) bool {
	switch t {
	case TypeSessionStarted, TypeSessionEnd, TypeSessionEnded,
		TypeSessionError, TypeSessionUsageTick,
		TypeSessionBalanceLow, TypeSessionBalanceRefilled,
		TypeSessionReconnected, TypeSessionTopup:
		return true
	}
	return false
}

// Decode parses a JSON frame into an Envelope.
func Decode(data []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, err
	}
	if env.Type == "" {
		return Envelope{}, errors.New("envelope: empty type")
	}
	return env, nil
}
