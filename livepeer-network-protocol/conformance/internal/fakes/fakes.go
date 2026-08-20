// Package fakes provides the conformance suite's fake job backend and
// fake session runner. Both are plain HTTP servers the broker-under-test
// is configured to talk to; both record what the broker sends so
// scenarios can assert on it.
package fakes

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sentinel secrets the descriptor leak-scan scenarios grep for in
// broker responses. They must never appear anywhere the broker emits.
const (
	PrivateSentinel     = "PRIVATE-SENTINEL-rt-4f1a"
	GrantSecretSentinel = "GRANT-SECRET-SENTINEL-gs-9b3c"
)

// ---------------------------------------------------------------------------
// listening

// Listen says where the fakes bind and how the broker under test reaches
// them. The zero value binds loopback on ephemeral ports, which is what
// auto mode and a same-host URL-mode run want. A broker in another
// container needs a routable bind host (0.0.0.0) and an advertise host it
// can resolve.
type Listen struct {
	// BindHost is the interface to bind. Default 127.0.0.1.
	BindHost string
	// AdvertiseHost is the host the broker under test uses to reach the
	// fakes. Defaults to BindHost, or to this machine's name when
	// BindHost is unspecified (0.0.0.0 / ::).
	AdvertiseHost string
	// BackendPort and RunnerPort pin the ports; 0 means ephemeral. Pin
	// them when the broker's config has to name the fakes up front.
	BackendPort int
	RunnerPort  int
}

func (l Listen) hosts() (bind, advertise string) {
	bind = l.BindHost
	if bind == "" {
		bind = "127.0.0.1"
	}
	if l.AdvertiseHost != "" {
		return bind, l.AdvertiseHost
	}
	// An unspecified bind address is not a usable URL host, so fall back
	// to this machine's name — inside a container that is the name the
	// embedded DNS resolves for peers on the same network.
	if ip := net.ParseIP(bind); ip != nil && ip.IsUnspecified() {
		if h, err := os.Hostname(); err == nil && h != "" {
			return bind, h
		}
		return bind, "127.0.0.1"
	}
	return bind, bind
}

// fakeServer is an HTTP server on an address the operator chose: it binds
// where asked and reports the URL the broker under test should use.
// httptest.Server cannot do that — it always binds loopback, which makes
// the fakes unreachable from another container.
type fakeServer struct {
	url string
	srv *http.Server
	ln  net.Listener
}

func serveFake(bind, advertise string, port int, h http.Handler) (*fakeServer, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	_, boundPort, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	s := &fakeServer{
		url: "http://" + net.JoinHostPort(advertise, boundPort),
		srv: &http.Server{Handler: h},
		ln:  ln,
	}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

func (s *fakeServer) Close() { _ = s.srv.Close() }

// ---------------------------------------------------------------------------
// job backend

// JobBackend is the fake paid-job workload.
type JobBackend struct {
	mu    sync.Mutex
	hits  int
	serve *fakeServer
}

// NewJobBackend starts the fake backend on the given address.
func NewJobBackend(l Listen) (*JobBackend, error) {
	b := &JobBackend{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.hits++
		b.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/slow") {
			// Long enough for a second request to arrive while this one
			// is still in flight.
			select {
			case <-time.After(3 * time.Second):
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"text":"slow"}],"usage":{"total_tokens":11}}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/longstream") {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			for i := 0; i < 40; i++ {
				fmt.Fprintf(w, "data: {\"chunk\":%d}\n\n", i)
				if flusher != nil {
					flusher.Flush()
				}
				select {
				case <-time.After(150 * time.Millisecond):
				case <-r.Context().Done():
					return
				}
			}
			fmt.Fprint(w, "data: {\"usage\":{\"total_tokens\":99}}\n\ndata: [DONE]\n\n")
			return
		}
		if strings.HasSuffix(r.URL.Path, "/error") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"backend exploded"}`)
			return
		}
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"chunk\":1}\n\n")
			fmt.Fprint(w, "data: {\"usage\":{\"total_tokens\":21}}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"text":"transcribed","usage":{"total_tokens":7}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"text":"ok"}],"usage":{"total_tokens":42}}`)
	})
	bind, advertise := l.hosts()
	srv, err := serveFake(bind, advertise, l.BackendPort, mux)
	if err != nil {
		return nil, err
	}
	b.serve = srv
	return b, nil
}

// URL is the backend base URL (point the happy-path offering here).
func (b *JobBackend) URL() string { return b.serve.url }

// ErrorURL is the always-500 route (point the error offering here).
func (b *JobBackend) ErrorURL() string { return b.serve.url + "/error" }

// SlowURL responds after ~3s, so a concurrent retry of the same request
// id arrives while the original is still in flight.
func (b *JobBackend) SlowURL() string { return b.serve.url + "/slow" }

// LongStreamURL emits SSE for ~6s, so a client can sever the body
// mid-stream.
func (b *JobBackend) LongStreamURL() string { return b.serve.url + "/longstream" }

// Hits returns how many requests the backend has served.
func (b *JobBackend) Hits() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hits
}

// Close shuts the server down.
func (b *JobBackend) Close() { b.serve.Close() }

// ---------------------------------------------------------------------------
// session runner

// CreateSeen records one create call the broker made.
type CreateSeen struct {
	SessionID     string          `json:"session_id"`
	WorkID        string          `json:"work_id"`
	SessionParams json.RawMessage `json:"session_params"`
	CallbackURL   string          `json:"callback_url"`
	CallbackToken string          `json:"callback_token"`
}

// SessionRunner is the fake paid-session runtime. Descriptor variants
// are selected by a "conformance_mode" field the scenario puts in
// session_params — the broker passes params through verbatim, so the
// fake sees it and misbehaves on demand.
type SessionRunner struct {
	mu         sync.Mutex
	creates    []CreateSeen
	terminated []string
	serve      *fakeServer
	httpc      *http.Client
}

// NewSessionRunner starts the fake runner on the given address.
func NewSessionRunner(l Listen) (*SessionRunner, error) {
	f := &SessionRunner{httpc: &http.Client{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions", f.handleCreate)
	mux.HandleFunc("GET /sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"runner_session_id":%q,"state":"active"}`, r.PathValue("id"))
	})
	mux.HandleFunc("DELETE /sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.terminated = append(f.terminated, r.PathValue("id"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	bind, advertise := l.hosts()
	srv, err := serveFake(bind, advertise, l.RunnerPort, mux)
	if err != nil {
		return nil, err
	}
	f.serve = srv
	return f, nil
}

func (f *SessionRunner) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req CreateSeen
	_ = json.NewDecoder(r.Body).Decode(&req)
	f.mu.Lock()
	f.creates = append(f.creates, req)
	f.mu.Unlock()

	var params map[string]any
	_ = json.Unmarshal(req.SessionParams, &params)
	mode, _ := params["conformance_mode"].(string)

	runtime := ""
	switch mode {
	case "unknown_key":
		runtime = `{"schema":"sfu-room/v1","public":{"url":"wss://sfu"},"surprise":{"x":1}}`
	case "schema_mismatch":
		runtime = `{"schema":"rtmp-hls/v1","public":{"url":"wss://sfu"}}`
	case "oversize":
		runtime = fmt.Sprintf(`{"schema":"sfu-room/v1","public":{"pad":%q}}`,
			strings.Repeat("x", 17*1024))
	case "malformed_grant":
		// Grant missing the required secret and expires_at.
		runtime = `{"schema":"sfu-room/v1","public":{"url":"wss://sfu","room":"r","mint_url":"https://sfu/mint"},
			"grants":[{"id":"g1","operations":["participant-token-mint"]}]}`
	case "rtmp-hls":
		runtime = fmt.Sprintf(`{
			"schema": "rtmp-hls/v1",
			"public": {"rtmp_url":"rtmp://ingest.example/live","hls_url":"https://play.example/m.m3u8",
			           "key_issue_url":"%s/keys","status_url":"%s/status"},
			"private": {"terminate_token": %q},
			"grants": [{"id":"g1","operations":["stream-key-issue"],"secret":%q,"expires_at":"2030-01-01T00:00:00Z"}]
		}`, f.serve.url, f.serve.url, PrivateSentinel, GrantSecretSentinel)
	case "scope-passthrough":
		runtime = fmt.Sprintf(`{
			"schema": "scope-passthrough/v1",
			"public": {"scope_url":"%s/scope","status_url":"%s/status"},
			"private": {"terminate_token": %q},
			"grants": [{"id":"g1","operations":["scope-api-access"],"secret":%q,"expires_at":"2030-01-01T00:00:00Z"}]
		}`, f.serve.url, f.serve.url, PrivateSentinel, GrantSecretSentinel)
	case "trickle-egress":
		runtime = fmt.Sprintf(`{
			"schema": "trickle-egress/v1",
			"public": {"control_url":"%s/control","preview_url":"%s/preview","status_url":"%s/status"},
			"private": {"terminate_token": %q},
			"grants": [{"id":"g1","operations":["control-attach"],"secret":%q,"expires_at":"2030-01-01T00:00:00Z"}]
		}`, f.serve.url, f.serve.url, f.serve.url, PrivateSentinel, GrantSecretSentinel)
	default:
		runtime = fmt.Sprintf(`{
			"schema": "sfu-room/v1",
			"public": {"url": "wss://sfu.example", "room": "rm_conf", "mint_url": "%s/mint"},
			"private": {"terminate_token": %q},
			"grants": [{"id":"g1","operations":["participant-token-mint"],"secret":%q,"expires_at":"2030-01-01T00:00:00Z"}]
		}`, f.serve.url, PrivateSentinel, GrantSecretSentinel)
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"runner_session_id":"rns_%s","runtime":%s}`, req.SessionID, runtime)
}

// URL is the runner base URL.
func (f *SessionRunner) URL() string { return f.serve.url }

// LastCreate returns the most recent create the broker sent.
func (f *SessionRunner) LastCreate() (CreateSeen, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.creates) == 0 {
		return CreateSeen{}, false
	}
	return f.creates[len(f.creates)-1], true
}

// Terminated returns the runner-session ids the broker terminated.
func (f *SessionRunner) Terminated() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.terminated...)
}

// PostEvent sends a runner event to the broker using the callback
// coordinates captured at create time. Returns status and body.
func (f *SessionRunner) PostEvent(cb CreateSeen, body string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodPost, cb.CallbackURL, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cb.CallbackToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.httpc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, nil
}

// Close shuts the server down.
func (f *SessionRunner) Close() { f.serve.Close() }
