package desiredstate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDurableRotationLostResponseRestartAndAckRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-credentials.json")
	initial := AgentCredentials{PoolID: "us", EnrollmentID: "host", Token: "old-token", AttachCredential: "old-attach", Generation: 1}
	if err := SaveAgentCredentials(path, initial); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	rotations := 0
	var requestID string
	loseResponse := true
	loseAck := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var input struct {
			RequestID string `json:"request_id"`
		}
		json.NewDecoder(r.Body).Decode(&input)
		switch {
		case strings.HasSuffix(r.URL.Path, "/agent-rotation"):
			stored, err := LoadAgentCredentials(path)
			if err != nil || stored.RequestID == "" {
				t.Error("request proof was not durable before mutation")
			}
			if r.Header.Get("Authorization") != "Bearer old-token" {
				http.Error(w, "wrong old token", 401)
				return
			}
			if requestID == "" {
				requestID = input.RequestID
				rotations++
			} else if input.RequestID != requestID {
				http.Error(w, "different rotation", 409)
				return
			}
			if loseResponse {
				loseResponse = false
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			json.NewEncoder(w).Encode(AgentCredentials{PoolID: "us", EnrollmentID: "host", Token: "new-token", AttachCredential: "new-attach", Generation: 2})
		case strings.HasSuffix(r.URL.Path, "/agent-rotation-ack"):
			stored, err := LoadAgentCredentials(path)
			if err != nil || stored.Token != "new-token" || stored.AttachCredential != "new-attach" || !stored.PendingAck {
				t.Error("ack before durable secret pair")
			}
			if r.Header.Get("Authorization") != "Bearer new-token" || input.RequestID != requestID {
				http.Error(w, "wrong ack", 401)
				return
			}
			if loseAck {
				loseAck = false
				http.Error(w, "ack response lost", 503)
				return
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := New(server.URL, "host", initial.Token, time.Second)
	if _, err := client.RotateAgentCredentials(context.Background(), path); err == nil {
		t.Fatal("lost response reported success")
	}
	disk, err := LoadAgentCredentials(path)
	if err != nil || disk.Token != initial.Token || disk.RequestID == "" || disk.PendingAck {
		t.Fatal("pending proof lost", err)
	}
	client = New(server.URL, "host", disk.Token, time.Second)
	state, err := client.RecoverAgentRotation(context.Background(), path)
	if err == nil || state.AttachCredential != "new-attach" {
		t.Fatal("ack failure hid usable persisted credentials", err)
	}
	disk, _ = LoadAgentCredentials(path)
	if !disk.PendingAck || disk.Token != "new-token" {
		t.Fatal("new token not durable before failed ack")
	}
	client = New(server.URL, "host", disk.Token, time.Second)
	state, err = client.RecoverAgentRotation(context.Background(), path)
	if err != nil || state.PendingAck || state.RequestID != "" {
		t.Fatal("restart ack failed", err)
	}
	if rotations != 1 {
		t.Fatal("rotation replay generated another secret")
	}
}
func TestAgentCredentialsCannotCrossEnrollmentOrFollowRedirect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")
	if err := SaveAgentCredentials(path, AgentCredentials{PoolID: "eu", EnrollmentID: "host-a", Token: "token", AttachCredential: "attach", Generation: 1}); err != nil {
		t.Fatal(err)
	}
	client := New("https://never-contact.invalid", "host-b", "token", time.Second)
	if _, err := client.RotateAgentCredentials(context.Background(), path); err == nil {
		t.Fatal("cross-enrollment file accepted")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("controller credential followed redirect") }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	client = New(redirect.URL, "host-a", "token", time.Second)
	if _, err := client.CurrentAgentCredentials(context.Background()); err == nil {
		t.Fatal("redirect accepted")
	}
}
