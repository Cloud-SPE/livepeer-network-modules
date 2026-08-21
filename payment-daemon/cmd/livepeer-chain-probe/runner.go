package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

// fakeRunner is the session backend the broker binds to. A paid-session
// cannot be opened without one, so the probe hosts its own rather than
// depending on a runtime being deployed.
//
// It binds a real address (not httptest's loopback-only listener) so the
// broker can reach it whether they share a host or not.
type fakeRunner struct {
	mu       sync.Mutex
	creates  []createSeen
	srv      *http.Server
	url      string
	terminat []string
}

type createSeen struct {
	SessionID     string          `json:"session_id"`
	WorkID        string          `json:"work_id"`
	SessionParams json.RawMessage `json:"session_params"`
	CallbackURL   string          `json:"callback_url"`
	CallbackToken string          `json:"callback_token"`
}

func startFakeRunner(bind string) (*fakeRunner, error) {
	f := &fakeRunner{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions", func(w http.ResponseWriter, r *http.Request) {
		var req createSeen
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.creates = append(f.creates, req)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"runner_session_id":"rns_%s","runtime":{
			"schema":"sfu-room/v1",
			"public":{"url":"wss://probe.invalid","room":"probe","mint_url":"%s/mint"},
			"private":{"terminate_token":"probe-terminate"},
			"grants":[{"id":"g1","operations":["participant-token-mint"],
			           "secret":"probe-grant-secret","expires_at":"2030-01-01T00:00:00Z"}]
		}}`, req.SessionID, f.url)
	})
	mux.HandleFunc("GET /sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"runner_session_id":%q,"state":"active"}`, r.PathValue("id"))
	})
	mux.HandleFunc("DELETE /sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.terminat = append(f.terminat, r.PathValue("id"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	ln, err := net.Listen("tcp", bind)
	if err != nil {
		return nil, err
	}
	f.url = "http://" + ln.Addr().String()
	f.srv = &http.Server{Handler: mux}
	go func() { _ = f.srv.Serve(ln) }()
	return f, nil
}

func (f *fakeRunner) lastCreate() (createSeen, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.creates) == 0 {
		return createSeen{}, false
	}
	return f.creates[len(f.creates)-1], true
}

func (f *fakeRunner) terminated() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.terminat)
}

// postEvent sends a runner event using the callback coordinates the
// broker handed over at create time.
func (f *fakeRunner) postEvent(cb createSeen, body string) (int, string, error) {
	req, err := http.NewRequest(http.MethodPost, cb.CallbackURL, strings.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+cb.CallbackToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	buf := new(strings.Builder)
	_, _ = fmt.Fprint(buf, readAll(resp.Body))
	return resp.StatusCode, buf.String(), nil
}

func (f *fakeRunner) close() {
	if f.srv != nil {
		_ = f.srv.Close()
	}
}
