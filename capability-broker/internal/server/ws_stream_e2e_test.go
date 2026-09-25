package server

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/workerconn"
)

// The real paid HTTP/middleware path and real WS broker forwarder must deliver
// the first fixture event before the runner is allowed to send terminal usage.
func TestPaidWebSocketStreamIsIncrementalAndSettles(t *testing.T) {
	t.Run("complete", func(t *testing.T) { testPaidWSStream(t, false) })
	t.Run("interrupted", func(t *testing.T) { testPaidWSStream(t, true) })
}
func testPaidWSStream(t *testing.T, interrupted bool) {
	var fixture struct {
		ResponseMode string              `json:"response_mode"`
		Headers      map[string][]string `json:"headers"`
		Chunks       []string            `json:"chunks"`
		Units        string              `json:"expected_work_units"`
	}
	raw, err := os.ReadFile("../../../livepeer-network-protocol/conformance/fixtures/websocket-response-stream.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if interrupted {
		fixture.Chunks[1] = strings.ReplaceAll(fixture.Chunks[1], "data: [DONE]\n\n", "")
	}
	ts, s := newJobOfferBrokerBare(t, nil, "", func(c *config.Config) { c.Offers[0].Capacity.MaxInFlight = 1 })
	_, enr, _ := adminReq(t, s, http.MethodPost, "/admin/v1/enroll", `{"host_id":"stream-host"}`, nil)
	token := enr["credential"].(map[string]any)["token"].(string)
	conn := dialAttach(t, ts)
	timeout := time.AfterFunc(10*time.Second, func() { _ = conn.Close() })
	defer timeout.Stop()
	var mu sync.Mutex
	send := func(m workerconn.TunnelMessage) error { mu.Lock(); defer mu.Unlock(); return conn.WriteJSON(m) }
	results := make(chan map[string]any, 4)
	var paid atomic.Bool
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	go func() {
		for {
			var m workerconn.TunnelMessage
			if err := conn.ReadJSON(&m); err != nil {
				return
			}
			if m.Type == "register_result" {
				var result map[string]any
				json.Unmarshal(m.Body, &result)
				results <- result
				continue
			}
			if m.Type != "request" {
				continue
			}
			go func(m workerconn.TunnelMessage) {
				if !paid.Load() {
					send(workerconn.TunnelMessage{Type: "response", ID: m.ID, StatusCode: 200, Headers: map[string][]string{"Content-Type": {"application/json"}}, BodyBase64: base64.StdEncoding.EncodeToString([]byte(`{"choices":[{"text":"ready"}],"usage":{"total_tokens":1}}`))})
					return
				}
				if m.ResponseMode != fixture.ResponseMode {
					t.Error("missing negotiated response mode")
					return
				}
				send(workerconn.TunnelMessage{Type: "response_start", ID: m.ID, StatusCode: 200, Headers: fixture.Headers})
				send(workerconn.TunnelMessage{Type: "response_chunk", ID: m.ID, Seq: 1, BodyBase64: base64.StdEncoding.EncodeToString([]byte(fixture.Chunks[0]))})
				select {
				case <-release:
				case <-time.After(5 * time.Second):
					return
				}
				send(workerconn.TunnelMessage{Type: "response_chunk", ID: m.ID, Seq: 2, BodyBase64: base64.StdEncoding.EncodeToString([]byte(fixture.Chunks[1]))})
				if interrupted {
					send(workerconn.TunnelMessage{Type: "response_error", ID: m.ID, Error: "upstream interrupted"})
				} else {
					send(workerconn.TunnelMessage{Type: "response_end", ID: m.ID, Seq: 2})
				}
			}(m)
		}
	}()
	// Serialize registration with response writes, just as the real agent does.
	doc := attachDoc(token, "stream-host", func(m map[string]any) {
		c := m["capabilities"].([]any)[0].(map[string]any)
		c["identity"] = map[string]any{"openai.model": "test-model"}
		c["transports"] = []any{"unary", "stream"}
	})
	if err := send(workerconn.TunnelMessage{Type: "register", ID: "register", Body: doc}); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-results:
		if r["document"] != "accepted" {
			t.Fatalf("attach=%v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attach timeout")
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(s.offersEngine.EligiblePairs("default")) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("runner never eligible")
		}
		time.Sleep(10 * time.Millisecond)
	}
	paid.Store(true)
	// jobReq sends normal streaming negotiation and a valid mock authorization.
	response := jobReq(t, ts, "incremental-ws", "text/event-stream")
	defer response.Body.Close()
	first := make([]byte, len(fixture.Chunks[0]))
	if _, err := io.ReadFull(response.Body, first); err != nil {
		t.Fatal(err)
	}
	if string(first) != fixture.Chunks[0] {
		t.Fatalf("first=%q", first)
	}
	if got := s.currentBackendInFlight("stream-host|chat"); got != 1 {
		t.Fatalf("stream occupancy=%d", got)
	}
	blocked := jobReq(t, ts, "stream-at-capacity", "text/event-stream")
	blocked.Body.Close()
	if blocked.StatusCode != 503 || blocked.Header.Get(livepeerheader.Backoff) == "" {
		t.Fatalf("at capacity: %d %v", blocked.StatusCode, blocked.Header)
	}
	unblock()
	rest, err := io.ReadAll(response.Body)
	if (err != nil) != interrupted {
		t.Fatalf("stream interruption=%v err=%v", interrupted, err)
	}
	if string(rest) != fixture.Chunks[1] {
		t.Fatalf("rest=%q", rest)
	}
	if got := response.Trailer.Get(livepeerheader.WorkUnits); !interrupted && got != fixture.Units {
		t.Fatalf("usage=%s", got)
	}
	if got := s.currentBackendInFlight("stream-host|chat"); got != 0 {
		t.Fatalf("completed stream occupancy=%d", got)
	}
	// Persisted outcome supports replay without re-running the model.
	// Aborted streams cannot deliver trailers; accounting is recovered by lookup.
	replay := jobReq(t, ts, "incremental-ws", "text/event-stream")
	defer replay.Body.Close()
	body, _ := io.ReadAll(replay.Body)
	if replay.Header.Get(livepeerheader.WorkUnits) != fixture.Units {
		t.Fatal("partial accounting was lost")
	}
	if !strings.Contains(string(body), `"replayed":true`) {
		t.Fatalf("replay=%s", body)
	}
}
