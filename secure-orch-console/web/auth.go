package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
)

const sessionCookieName = "secure_orch_session"

type actorContextKey struct{}

var errSessionAlreadyActive = errors.New("another operator session is already active")

type authManager struct {
	mu      sync.Mutex
	tokens  map[string]struct{}
	current *session
}

type session struct {
	id    string
	actor string
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
	return &authManager{tokens: allowed}
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
	if a.current != nil {
		return "", errSessionAlreadyActive
	}
	id, err := randomSessionID()
	if err != nil {
		return "", err
	}
	a.current = &session{id: id, actor: actor}
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
	if a.current == nil || a.current.id != sessionID {
		return "", false
	}
	return a.current.actor, true
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
