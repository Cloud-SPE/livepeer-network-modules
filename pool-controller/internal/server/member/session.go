package member

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const memberSessionCookieName = "pool_member_session"

const memberSessionTTL = 24 * time.Hour

type SessionAuth struct {
	mu       sync.Mutex
	sessions map[string]memberSession
	now      func() time.Time
}

type memberSession struct {
	memberID  string
	expiresAt time.Time
}

func NewSessionAuth() *SessionAuth {
	return &SessionAuth{sessions: make(map[string]memberSession), now: time.Now}
}

func (a *SessionAuth) Create(memberID string) (string, error) {
	if a == nil {
		return "", nil
	}
	id, err := randomSessionID()
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions[id] = memberSession{memberID: memberID, expiresAt: a.now().Add(memberSessionTTL)}
	return id, nil
}

func (a *SessionAuth) MemberID(sessionID string) (string, bool) {
	if a == nil || sessionID == "" {
		return "", false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[sessionID]
	if !ok {
		return "", false
	}
	if !a.now().Before(session.expiresAt) {
		delete(a.sessions, sessionID)
		return "", false
	}
	return session.memberID, true
}

func setMemberSessionCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     memberSessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(memberSessionTTL / time.Second),
	})
}

func randomSessionID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
