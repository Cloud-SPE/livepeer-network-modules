package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
)

// Optional cross-component acceptance gate. Build the real member-agent binary
// and pass its absolute path in LNM_STREAM_AGENT_BINARY; no deployed services,
// GPU, wallet or real funds are involved.
func TestRealMemberAgentWebSocketStreaming(t *testing.T) {
	binary := os.Getenv("LNM_STREAM_AGENT_BINARY")
	if binary == "" {
		t.Skip("set LNM_STREAM_AGENT_BINARY for real-agent acceptance")
	}
	ts, s := newJobOfferBrokerBare(t, nil, "")
	_, enr, _ := adminReq(t, s, http.MethodPost, "/admin/v1/enroll", `{"host_id":"wire-agent"}`, nil)
	token := enr["credential"].(map[string]any)["token"].(string)
	var document map[string]any
	json.Unmarshal(attachDoc(token, "wire-agent", nil), &document)
	contract := document["capabilities"].([]any)[0].(map[string]any)
	delete(contract, "local_id")
	contract["identity"] = map[string]any{"openai.model": "test-model"}
	contract["transports"] = []string{"unary", "stream"}
	var paid atomic.Bool
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/livepeer-runner" {
			json.NewEncoder(w).Encode(contract)
			return
		}
		if !paid.Load() {
			io.WriteString(w, `{"choices":[{"text":"ready"}],"usage":{"total_tokens":1}}`)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["stream"] != true {
			t.Error("stream:true request body was not preserved")
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Error("stream negotiation lost")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, "data: {\"usage\":{\"total_tokens\":21}}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	cmd := exec.Command(binary)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LIVEPEER_HOST_ID=wire-agent", "LIVEPEER_BROKER_URL=" + ts.URL, "LIVEPEER_ATTACH_CREDENTIAL=" + token, "LIVEPEER_RUNNER_URL=" + upstream.URL}
	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for len(s.offersEngine.EligiblePairs("default")) != 1 {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("agent not eligible: %s", logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	paid.Store(true)
	timeout := time.AfterFunc(5*time.Second, func() { _ = cmd.Process.Kill() })
	defer timeout.Stop()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/job", strings.NewReader(`{"model":"test-model","messages":[],"stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream") // BlueClaw supplies this translation.
	req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	req.Header.Set(livepeerheader.Offering, "default")
	req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	req.Header.Set(livepeerheader.RequestID, "real-agent-stream")
	req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("stub-payment")))
	setJobTestAuthorization(t, req, ts.URL)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	first := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(response.Body, first); err != nil {
		t.Fatal(err)
	}
	if string(first) != "data: first\n\n" {
		t.Fatalf("first=%q", first)
	}
	// The actual model HTTP response is still open here: both deployed binaries'
	// transport implementations have forwarded the first event before EOF.
	unblock()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	if response.Trailer.Get(livepeerheader.WorkUnits) != "21" {
		t.Fatalf("lost usage: %v", response.Trailer)
	}
}
