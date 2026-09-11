package certification

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The usage tap is how a session capability proves it can be billed
// (certification-steps §3.3, session form).
//
// A job runner reports its usage in the response body, so the usage
// step can read it straight off the exchange. A SESSION runner does
// not: it reports usage asynchronously, by POSTing events to a callback
// the broker hands it at create time (paid-session/v1 §7.1). During
// certification there was no such callback — the certification path
// deliberately does not mint a paid session — so the runner had nowhere
// to report, and the usage step recorded "not implemented". A session
// capability could therefore certify and be advertised without anyone
// having checked that it can be billed at all, which is the one thing
// certification exists to check.
//
// The tap is that callback, scoped to a single certification run. The
// URL is opaque to the runner — it posts wherever it was told, with the
// token it was given — so this exercises exactly the runner-side path
// that production uses, without minting a session the payment machinery
// would then have to account for.
//
// It is deliberately not the paid /v1/session/{id}/events surface. That
// handler debits a real encumbrance against a real session record; a
// certification run has neither, and inventing them to satisfy the
// handler would put unpayable sessions in the store for every run.

// UsageEvent is one usage report from a runner under certification.
type UsageEvent struct {
	Sequence  uint64    `json:"sequence"`
	EventType string    `json:"event_type"`
	Unit      string    `json:"unit"`
	Total     uint64    `json:"total"`
	At        time.Time `json:"at"`
}

// maxTapEvents bounds one tap's memory. A runner that reports every
// token would otherwise pin the broker's heap for the length of the
// step, and no certification decision needs more than the last report:
// usage totals are cumulative (paid-session/v1 §7.2), so the highest
// total is the answer regardless of how many arrived.
const maxTapEvents = 256

// maxTapAge is how long an unclosed tap survives. Every step in a run
// is bounded by its own timeout, so a tap older than this belongs to a
// run that died between opening a session and reading its usage.
const maxTapAge = 30 * time.Minute

// tap collects the usage a single certification session reported.
type tap struct {
	tokenHash [32]byte
	runID     string
	sessionID string
	openedAt  time.Time

	mu     sync.Mutex
	events []UsageEvent
	// highest is the greatest cumulative total seen, which is what the
	// usage step asks for. Kept separately so an out-of-order or
	// duplicate event cannot lower it.
	highest uint64
	unit    string
	// highestAt is when the deciding event arrived, which the step
	// reports as evidence (certification-steps §3.3).
	highestAt time.Time
}

// usageTaps holds every open tap, keyed by its id.
type usageTaps struct {
	mu   sync.Mutex
	taps map[string]*tap
}

func newUsageTaps() *usageTaps { return &usageTaps{taps: map[string]*tap{}} }

// open mints a tap and returns its id and bearer token. The token is
// returned once and stored only as a hash, so a leaked broker heap does
// not hand out the ability to forge usage.
func (u *usageTaps) open(runID, sessionID string, now time.Time) (id, token string, err error) {
	idBytes := make([]byte, 16)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(idBytes); err != nil {
		return "", "", fmt.Errorf("certification: mint tap id: %w", err)
	}
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", "", fmt.Errorf("certification: mint tap token: %w", err)
	}
	id = "certtap_" + hex.EncodeToString(idBytes)
	token = "certcb_" + hex.EncodeToString(tokenBytes)
	// Sweep here rather than from a goroutine: taps only ever appear on
	// open, so opening is the only moment the map can grow, and a
	// broker running no certifications needs no timer to prove it holds
	// nothing.
	u.sweep(now, maxTapAge)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.taps[id] = &tap{tokenHash: hashToken(token), runID: runID, sessionID: sessionID, openedAt: now}
	return id, token, nil
}

// close removes a tap and returns what it collected. A tap that is
// never closed — a run that panics or is cancelled mid-step — is
// reaped by sweep.
func (u *usageTaps) close(id string) observed {
	u.mu.Lock()
	t := u.taps[id]
	delete(u.taps, id)
	u.mu.Unlock()
	if t == nil {
		return observed{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return observed{highest: t.highest, unit: t.unit, at: t.highestAt, count: len(t.events)}
}

// observed is what a tap collected.
type observed struct {
	highest uint64
	unit    string
	at      time.Time
	count   int
}

// Record ingests one usage event. It reports whether the tap exists and
// the token matches; a caller must not distinguish the two, so that an
// unknown id and a wrong token are the same answer to a prober.
func (u *usageTaps) record(id, token string, ev UsageEvent) bool {
	u.mu.Lock()
	t := u.taps[id]
	u.mu.Unlock()
	if t == nil {
		return false
	}
	want := hashToken(token)
	if subtle.ConstantTimeCompare(t.tokenHash[:], want[:]) != 1 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.events) < maxTapEvents {
		t.events = append(t.events, ev)
	}
	if ev.Total > t.highest || t.highestAt.IsZero() {
		t.highest = ev.Total
		t.unit = ev.Unit
		t.highestAt = ev.At
	}
	return true
}

// peek reads a tap without closing it, so a usage step can wait for
// evidence to arrive rather than deciding on whatever happened to have
// landed at the instant it ran.
func (u *usageTaps) peek(id string) observed {
	u.mu.Lock()
	t := u.taps[id]
	u.mu.Unlock()
	if t == nil {
		return observed{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return observed{highest: t.highest, unit: t.unit, at: t.highestAt, count: len(t.events)}
}

// sweep drops taps older than max age. Nothing else would: a tap is
// closed by the step that opened it, and a run killed between open and
// close leaves one behind.
func (u *usageTaps) sweep(now time.Time, maxAge time.Duration) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	dropped := 0
	for id, t := range u.taps {
		if now.Sub(t.openedAt) > maxAge {
			delete(u.taps, id)
			dropped++
		}
	}
	return dropped
}

func hashToken(token string) [32]byte { return sha256.Sum256([]byte(token)) }

// TapPathPrefix is the URL space certification callbacks live in. The
// server owns the route; this constant is what builds the URL handed to
// the runner, so the two cannot drift.
const TapPathPrefix = "/internal/v1/certification/usage/"

// TapURL is the callback a runner under certification posts to.
func TapURL(baseURL, tapID string) string {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		return ""
	}
	return base + TapPathPrefix + tapID
}

// RecordUsageEvent ingests a runner's usage callback for an open
// certification session. False means unknown tap or bad token, which
// the caller must report identically.
func (e *Engine) RecordUsageEvent(tapID, token string, ev UsageEvent) bool {
	if e.taps == nil {
		return false
	}
	return e.taps.record(tapID, token, ev)
}
