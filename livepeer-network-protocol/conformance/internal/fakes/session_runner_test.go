package fakes

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionRunnerProbeControlForwardsWithoutExposingCallbackToken(t *testing.T) {
	var receivedBody, receivedAuth string
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("forwarded"))
	}))
	defer callback.Close()

	runner, err := NewSessionRunner(Listen{})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	create := `{"session_id":"sess-1","work_id":"auth-1","session_params":{},"callback_url":` + quoteJSON(callback.URL) + `,"callback_token":"secret-token"}`
	resp, err := http.Post(runner.URL()+"/sessions", "application/json", strings.NewReader(create))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	event := `{"event_id":"event-1","sequence":1,"event_type":"session.usage.tick","usage":{"unit":"seconds","total":9}}`
	resp, err = http.Post(runner.URL()+"/__livepeer_probe/event", "application/json", strings.NewReader(event))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted || string(body) != "forwarded" {
		t.Fatalf("control response status=%d body=%q", resp.StatusCode, body)
	}
	if receivedAuth != "Bearer secret-token" || receivedBody != event {
		t.Fatalf("callback auth=%q body=%q", receivedAuth, receivedBody)
	}
	if strings.Contains(string(body), "secret-token") {
		t.Fatal("control response exposed callback token")
	}
}

func quoteJSON(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
