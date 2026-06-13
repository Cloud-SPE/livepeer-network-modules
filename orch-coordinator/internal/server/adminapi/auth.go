package adminapi

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

const sessionCookieName = "orch_coordinator_session"

const (
	sessionAbsoluteTTL = 4 * time.Hour
	sessionIdleTTL     = 30 * time.Minute
)

type actorContextKey struct{}

var errSessionAlreadyActive = errors.New("another live operator session is already active; wait for idle timeout or log out from the active browser")

type authManager struct {
	mu      sync.Mutex
	tokens  map[string]struct{}
	current *session
	now     func() time.Time
}

type session struct {
	id         string
	actor      string
	createdAt  time.Time
	lastSeenAt time.Time
}

func newAuthManager(tokens []string) *authManager {
	if len(tokens) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		allowed[token] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil
	}
	return &authManager{tokens: allowed, now: time.Now}
}

func (a *authManager) login(token, actor string) (string, error) {
	if a == nil {
		return "", nil
	}
	token = strings.TrimSpace(token)
	actor = strings.TrimSpace(actor)
	if token == "" || actor == "" {
		return "", errors.New("admin token and actor are required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.tokens[token]; !ok {
		return "", errors.New("invalid admin token")
	}
	a.reapExpiredLocked()
	if a.current != nil {
		return "", errSessionAlreadyActive
	}
	id, err := randomSessionID()
	if err != nil {
		return "", err
	}
	now := a.now()
	a.current = &session{id: id, actor: actor, createdAt: now, lastSeenAt: now}
	return id, nil
}

func (a *authManager) logout(sessionID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.current != nil && a.current.id == sessionID {
		a.current = nil
	}
}

func (a *authManager) actor(sessionID string) (string, bool) {
	if a == nil {
		return "", true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reapExpiredLocked()
	if a.current == nil || a.current.id != sessionID {
		return "", false
	}
	now := a.now()
	a.current.lastSeenAt = now
	return a.current.actor, true
}

func (a *authManager) reapExpiredLocked() {
	if a == nil || a.current == nil {
		return
	}
	now := a.now()
	if now.Sub(a.current.createdAt) >= sessionAbsoluteTTL || now.Sub(a.current.lastSeenAt) >= sessionIdleTTL {
		a.current = nil
	}
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	if s.auth == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			s.redirectToLogin(w, r)
			return
		}
		actor, ok := s.auth.actor(cookie.Value)
		if !ok {
			clearSessionCookie(w)
			s.redirectToLogin(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), actorContextKey{}, actor)
		next(w, r.WithContext(ctx))
	}
}

// agentActor is the audit identity recorded when a request was
// admitted by the agent bearer token instead of an operator session.
const agentActor = "agent"

// requireAuthOrAgent admits either a logged-in operator session or
// the secure-orch agent's bearer token (plan 0042 §5.2). The bearer
// only keeps anonymous traffic off the endpoint and identifies the
// agent in audit; the manifest signature remains the real content
// authentication. A presented-but-wrong bearer is rejected outright —
// it never falls through to the cookie flow.
func (s *Server) requireAuthOrAgent(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token, ok := bearerToken(r); ok {
			if s.agentToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.agentToken)) == 1 {
				ctx := context.WithValue(r.Context(), actorContextKey{}, agentActor)
				next(w, r.WithContext(ctx))
				return
			}
			http.Error(w, "invalid agent token", http.StatusUnauthorized)
			return
		}
		s.requireAuth(next)(w, r)
	}
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):]), true
	}
	return "", false
}

func actorFromRequest(r *http.Request) string {
	actor, _ := r.Context().Value(actorContextKey{}).(string)
	return actor
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func (s *Server) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

func randomSessionID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
