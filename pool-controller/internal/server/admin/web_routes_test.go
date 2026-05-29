package admin

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
)

// newConsoleServer registers the admin routes (UI + API) with a session
// manager whose token is the given value. An empty token means login is
// disabled (open mode).
func newConsoleServer(t *testing.T, token string) (*httptest.Server, *SessionAuth) {
	t.Helper()
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })

	sess := NewSessionAuth(func() string { return token })
	mux := http.NewServeMux()
	Register(mux, Deps{
		Repo:     stateRepo,
		WrapAuth: func(next http.HandlerFunc) http.HandlerFunc { return next },
		Session:  sess,
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, sess
}

func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func mustPostForm(t *testing.T, c *http.Client, target string, vals url.Values) *http.Response {
	t.Helper()
	resp, err := c.PostForm(target, vals)
	if err != nil {
		t.Fatalf("POST %s: %v", target, err)
	}
	return resp
}

func TestConsoleRequiresLogin(t *testing.T) {
	srv, _ := newConsoleServer(t, "secret")
	resp, err := noRedirectClient().Get(srv.URL + "/admin")
	if err != nil {
		t.Fatalf("GET /admin error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /admin status = %d; want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/admin/login" {
		t.Fatalf("redirect location = %q; want /admin/login", loc)
	}
}

func TestConsoleLoginFlow(t *testing.T) {
	srv, _ := newConsoleServer(t, "secret")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// 1. login page renders with both fields.
	resp, err := client.Get(srv.URL + "/admin/login")
	if err != nil {
		t.Fatalf("GET /admin/login error = %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `name="admin_token"`) || !strings.Contains(body, `name="actor"`) {
		t.Fatalf("login page status=%d body=%s", resp.StatusCode, body)
	}

	// 2. wrong token is rejected.
	resp = mustPostForm(t, client, srv.URL+"/admin/login", url.Values{"admin_token": {"nope"}, "actor": {"mike"}})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "invalid admin token") {
		t.Fatalf("wrong-token login status=%d body=%s", resp.StatusCode, body)
	}

	// 3. correct token + actor sets a session cookie and redirects.
	resp = mustPostForm(t, client, srv.URL+"/admin/login", url.Values{"admin_token": {"secret"}, "actor": {"mike"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin" {
		t.Fatalf("login status=%d loc=%s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()
	u, _ := url.Parse(srv.URL)
	if len(jar.Cookies(u)) == 0 {
		t.Fatal("no session cookie set after login")
	}

	// 4. authed page shows the actor + logout and no pasted-token field.
	resp, err = client.Get(srv.URL + "/admin")
	if err != nil {
		t.Fatalf("GET /admin (authed) error = %v", err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "mike") || !strings.Contains(body, "/admin/logout") {
		t.Fatalf("authed /admin status=%d body=%s", resp.StatusCode, body)
	}
	if strings.Contains(body, `id="token"`) {
		t.Fatal("authed page still renders the pasted-token field")
	}

	// 5. a second login while one is active is rejected (single active session).
	fresh := noRedirectClient()
	resp = mustPostForm(t, fresh, srv.URL+"/admin/login", url.Values{"admin_token": {"secret"}, "actor": {"other"}})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "already active") {
		t.Fatalf("second login status=%d body=%s", resp.StatusCode, body)
	}

	// 6. logout clears the session and redirects to login.
	resp = mustPostForm(t, client, srv.URL+"/admin/logout", url.Values{})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/login" {
		t.Fatalf("logout status=%d loc=%s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()
}

func TestConsolePagesRenderWithActiveNav(t *testing.T) {
	srv, sess := newConsoleServer(t, "secret")
	sid, err := sess.Login("secret", "mike")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	client := noRedirectClient()

	pages := []struct{ path, href, marker string }{
		{"/admin", "/admin", `id="ovOffers"`},
		{"/admin/offers", "/admin/offers", `id="offerId"`},
		{"/admin/join-requests", "/admin/join-requests", `id="joinRequests"`},
		{"/admin/members", "/admin/members", `id="members"`},
		{"/admin/assignments", "/admin/assignments", `id="assignmentCandidates"`},
		{"/admin/broker-runtime", "/admin/broker-runtime", `id="runtimeYaml"`},
		{"/admin/audit", "/admin/audit", `id="auditEvents"`},
	}
	for _, p := range pages {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+p.path, nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sid})
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s error = %v", p.path, err)
		}
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d; want 200", p.path, resp.StatusCode)
		}
		if !strings.Contains(body, p.marker) {
			t.Fatalf("GET %s missing marker %q", p.path, p.marker)
		}
		// exactly one nav item is active, and it is this page's item.
		if got := strings.Count(body, "sidebar-item active"); got != 1 {
			t.Fatalf("GET %s active nav count = %d; want 1", p.path, got)
		}
		wantActive := fmt.Sprintf(`href=%q class="sidebar-item active"`, p.href)
		if !strings.Contains(body, wantActive) {
			t.Fatalf("GET %s missing active nav anchor %q", p.path, wantActive)
		}
	}
}

func TestConsoleOpenModeSkipsLogin(t *testing.T) {
	srv, _ := newConsoleServer(t, "") // empty token => login disabled
	client := noRedirectClient()

	// pages render directly, no redirect to login.
	resp, err := client.Get(srv.URL + "/admin")
	if err != nil {
		t.Fatalf("GET /admin error = %v", err)
	}
	_ = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open-mode GET /admin status = %d; want 200", resp.StatusCode)
	}

	// the login route bounces back to the console.
	resp, err = client.Get(srv.URL + "/admin/login")
	if err != nil {
		t.Fatalf("GET /admin/login error = %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin" {
		t.Fatalf("open-mode GET /admin/login status=%d loc=%s", resp.StatusCode, resp.Header.Get("Location"))
	}
}
