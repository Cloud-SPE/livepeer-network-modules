package sessioncontrolexternalmedia

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
)

// ServeProxy handles /_scope/{session_id}/{path...} HTTP requests.
// It authorises against the live session record, strips Livepeer-*
// request headers, forwards the remaining request to the workload
// backend, and short-circuits the capability-declared session-start /
// session-stop paths to drive the broker's session lifecycle.
//
// The proxy does NOT carry media bytes — media flows browser ↔
// Cloudflare TURN ↔ backend directly, out of the broker entirely.
func (d *Driver) ServeProxy(w http.ResponseWriter, r *http.Request) {
	sessID := r.PathValue("session_id")
	if sessID == "" {
		http.Error(w, "missing session_id", http.StatusNotFound)
		return
	}
	rec := d.store.Get(sessID)
	if rec == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	backendURL, err := url.Parse(rec.BackendURL)
	if err != nil || backendURL.Host == "" {
		http.Error(w, "backend url invalid", http.StatusInternalServerError)
		return
	}

	// Rewrite the request URL so the path is just the backend-relative
	// portion (everything after /_scope/<session_id>).
	prefix := scopeURLPath(sessID)
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.Error(w, "proxy prefix mismatch", http.StatusNotFound)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	// Re-prepend a leading slash so the rest is absolute on the backend.
	backendPath := "/" + rest

	// Short-circuit: session-start. Start the seconds-elapsed clock at
	// first contact, emit session.started over the control-WS. The
	// request itself still flows through to the backend so it can
	// actually start the session there.
	if rec.SessionStartPath != "" && backendPath == rec.SessionStartPath && r.Method == http.MethodPost {
		if rec.MarkStarted(time.Now()) {
			d.emitLifecycle(rec, "session.started", nil)
		}
	}

	// Short-circuit: session-stop. After forwarding, tear the session
	// down. We forward first so the backend gets a clean stop signal,
	// then mark the record closed.
	endAfter := rec.SessionStopPath != "" && backendPath == rec.SessionStopPath && r.Method == http.MethodPost

	// Build the outbound request.
	outURL := *backendURL
	outURL.Path = strings.TrimRight(outURL.Path, "/") + backendPath
	outURL.RawQuery = r.URL.RawQuery

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL = &outURL
			req.Host = backendURL.Host
			stripLivepeerHeaders(req.Header)
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, e error) {
			// Backend unreachable. The disconnect watchdog
			// will pick this up; here we just surface a 502
			// to the caller.
			http.Error(rw, "backend unreachable: "+e.Error(), http.StatusBadGateway)
		},
		Transport: &http.Transport{
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}

	proxy.ServeHTTP(w, r)

	if endAfter {
		// Forwarded; now drive teardown.
		d.teardown(rec, "backend_session_stop")
	}
}

// stripLivepeerHeaders removes the wire-protocol headers from a request
// before it leaves the broker. The backend has no business seeing
// Livepeer-Capability / Offering / Payment / Mode / Spec-Version.
func stripLivepeerHeaders(h http.Header) {
	h.Del(livepeerheader.Capability)
	h.Del(livepeerheader.Offering)
	h.Del(livepeerheader.Payment)
	h.Del(livepeerheader.Mode)
	h.Del(livepeerheader.SpecVersion)
}
