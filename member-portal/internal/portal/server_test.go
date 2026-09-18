package portal

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestBrowserOriginCookieAndRestartSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "portal.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	server := &Server{Store: store, Origin: "https://portal.example"}
	handler, err := server.Handler()
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://portal.example"+path, strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	key, _ := crypto.GenerateKey()
	wallet := crypto.PubkeyToAddress(key.PublicKey).Hex()
	body := `{"wallet":"` + wallet + `"}`
	if w := request("POST", "/api/auth/challenge", body, "https://evil.example", nil); w.Code != 403 {
		t.Fatal(w.Code)
	}
	w := request("POST", "/api/auth/challenge", body, server.Origin, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var c Challenge
	json.Unmarshal(w.Body.Bytes(), &c)
	sig, _ := crypto.Sign(accounts.TextHash([]byte(c.Message)), key)
	w = request("POST", "/api/auth/login", `{"id":"`+c.ID+`","signature":"`+hex.EncodeToString(sig)+`"}`, server.Origin, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("cookie missing")
	}
	cookie := cookies[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("cookie not hardened")
	}
	store.Close()
	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	server.Store = store
	if w = request("GET", "/api/session", "", "", cookie); w.Code != 200 || !strings.Contains(w.Body.String(), strings.ToLower(wallet)) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("POST", "/api/auth/logout", "", "", cookie); w.Code != 403 {
		t.Fatal("logout CSRF accepted")
	}
	if w = request("POST", "/api/auth/logout", "", server.Origin, cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if _, err := store.Session(cookie.Value, time.Now()); err == nil {
		t.Fatal("logout did not revoke")
	}
}
func TestMemberProxyAllowlist(t *testing.T) {
	for _, path := range []string{"/admin/v1/payouts", "/member/v1/auth/verify", "/member/v1/enrollments/abc/desired-state", "/member/v1/enrollments/abc/bundle", "/member/v1/enrollments/abc/agent-credentials", "/member/v1/enrollments/abc/agent-rotation", "/member/v1/enrollments/abc/agent-rotation-ack", "/member/v1/../admin/v1/payouts", "/member/v1/enrollments/%2e%2e/rotate"} {
		for _, method := range []string{"GET", "POST"} {
			if allowedMemberPath(method, path) {
				t.Fatal(method, path)
			}
		}
	}
	if allowedMemberPath("POST", "/member/v1/enrollments/abc/status") {
		t.Fatal("member can impersonate agent")
	}
	if !allowedMemberPath("POST", "/member/v1/join") || !allowedMemberPath("POST", "/member/v1/enrollments/abc/rotate") {
		t.Fatal("member actions unavailable")
	}
}
