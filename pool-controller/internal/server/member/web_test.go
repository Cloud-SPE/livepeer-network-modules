package member

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The portal is the only thing a member ever sees, so a template that
// does not parse is a member who cannot use the pool at all. Loading
// them here means that fails in CI rather than at someone's first
// sign-in.
func TestPortalTemplatesLoadAndRender(t *testing.T) {
	templates, err := loadPortalTemplates()
	if err != nil {
		t.Fatalf("loadPortalTemplates() error = %v", err)
	}
	for _, page := range portalPages {
		tmpl, ok := templates[page]
		if !ok {
			t.Fatalf("page %q did not load", page)
		}
		rec := httptest.NewRecorder()
		renderPortal(rec, tmpl, "layout", portalPageData{
			Title: "T", ActivePage: page, Version: "test", MemberAddress: "0xabc",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("page %q rendered %d: %s", page, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		// One active nav item, or the shell is lying about where the
		// member is.
		if got := strings.Count(body, "sidebar-item active"); got != 1 {
			t.Fatalf("page %q has %d active nav items, want exactly 1", page, got)
		}
		// The portal must never link the operator console.
		if strings.Contains(body, "/admin") {
			t.Fatalf("page %q links an admin path:\n%s", page, body)
		}
	}
	if _, ok := templates["signin"]; !ok {
		t.Fatal("sign-in page did not load")
	}
}

// A signed-out browser gets sent somewhere useful rather than an empty
// shell it cannot populate.
func TestPortalPagesRedirectWhenSignedOut(t *testing.T) {
	mux := http.NewServeMux()
	registerPortalRoutes(mux, Deps{Sessions: NewSessionAuth()})
	for _, path := range []string{"/member", "/member/hosts", "/member/earnings", "/member/settings"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%s returned %d, want a redirect to sign-in", path, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "/member/signin" {
			t.Fatalf("%s redirected to %q", path, got)
		}
	}
}
