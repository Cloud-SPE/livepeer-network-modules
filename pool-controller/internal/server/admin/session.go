package admin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SessionCookieName is the operator-session cookie for the pool console. It is
// exported so the /admin/v1 API auth wrapper can accept the same session.
const SessionCookieName = "pool_controller_session"

const (
	sessionAbsoluteTTL = 4 * time.Hour
	sessionIdleTTL     = 30 * time.Minute
)

type actorContextKey struct{}

// ErrSessionAlreadyActive mirrors the trust-spine consoles: a single live
// operator session at a time.
var ErrSessionAlreadyActive = errors.New("another live operator session is already active; wait for idle timeout or log out from the active browser")

// SessionAuth issues and validates operator sessions. It validates the
// supplied admin token against the controller's current token via tokenFn, so
// token rotation on config reload is honored without restarting the session
// manager.
type SessionAuth struct {
	mu      sync.Mutex
	tokenFn func() string
	current *session
	now     func() time.Time
}

type session struct {
	id         string
	actor      string
	createdAt  time.Time
	lastSeenAt time.Time
}

// NewSessionAuth builds a session manager. tokenFn must return the current
// admin bearer token (empty string means admin auth is disabled / open).
func NewSessionAuth(tokenFn func() string) *SessionAuth {
	if tokenFn == nil {
		tokenFn = func() string { return "" }
	}
	return &SessionAuth{tokenFn: tokenFn, now: time.Now}
}

// Enabled reports whether login is required (i.e. an admin token is set).
func (a *SessionAuth) Enabled() bool {
	if a == nil {
		return false
	}
	return strings.TrimSpace(a.tokenFn()) != ""
}

// Login validates the token+actor and, on success, returns a new session id.
func (a *SessionAuth) Login(token, actor string) (string, error) {
	if a == nil {
		return "", nil
	}
	token = strings.TrimSpace(token)
	actor = strings.TrimSpace(actor)
	if token == "" || actor == "" {
		return "", errors.New("admin token and actor are required")
	}
	want := strings.TrimSpace(a.tokenFn())
	if want == "" || subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
		return "", errors.New("invalid admin token")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reapExpiredLocked()
	if a.current != nil {
		return "", ErrSessionAlreadyActive
	}
	id, err := randomSessionID()
	if err != nil {
		return "", err
	}
	now := a.now()
	a.current = &session{id: id, actor: actor, createdAt: now, lastSeenAt: now}
	return id, nil
}

// Logout clears the session if the supplied id is the current one.
func (a *SessionAuth) Logout(sessionID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.current != nil && a.current.id == sessionID {
		a.current = nil
	}
}

// Actor returns the actor for a live session id, refreshing its idle timer.
func (a *SessionAuth) Actor(sessionID string) (string, bool) {
	if a == nil {
		return "", false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reapExpiredLocked()
	if a.current == nil || a.current.id != sessionID {
		return "", false
	}
	a.current.lastSeenAt = a.now()
	return a.current.actor, true
}

func (a *SessionAuth) reapExpiredLocked() {
	if a.current == nil {
		return
	}
	now := a.now()
	if now.Sub(a.current.createdAt) >= sessionAbsoluteTTL || now.Sub(a.current.lastSeenAt) >= sessionIdleTTL {
		a.current = nil
	}
}

func actorFromRequest(r *http.Request) string {
	actor, _ := r.Context().Value(actorContextKey{}).(string)
	return actor
}

func withActor(r *http.Request, actor string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), actorContextKey{}, actor))
}

func setSessionCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionAbsoluteTTL / time.Second),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func randomSessionID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
