package member

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/memberenrollment"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestAgentRotationHTTPRecoversAfterSyncFailureAndControllerRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := repo.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	if err := store.PutHostEnrollment(types.HostEnrollment{ID: "host", PoolID: store.PoolID(), EnrollmentTokenHash: memberenrollment.HashToken("old"), BrokerSessionCredential: "old-attach", CredentialGeneration: 1, Status: types.HostEnrollmentActive}); err != nil {
		t.Fatal(err)
	}
	syncFailed := true
	makeMux := func() *http.ServeMux {
		mux := http.NewServeMux()
		Register(mux, Deps{Repo: store, RefreshTerms: func() error {
			if syncFailed {
				return fmt.Errorf("source offline")
			}
			return nil
		}})
		return mux
	}
	mux := makeMux()
	proof := strings.Repeat("a", 64)
	call := func(method, action, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://region.example/member/v1/enrollments/host/"+action, strings.NewReader(`{"request_id":"`+proof+`"}`))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "agent-rotation", "old"); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("GET", "agent-credentials", "old"); w.Code != 401 {
		t.Fatal("old credential remained active")
	}
	store.Close()
	store, err = repo.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	syncFailed = false
	mux = makeMux()
	w := call("POST", "agent-rotation", "old")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var pair repo.AgentCredentialPair
	if err := json.Unmarshal(w.Body.Bytes(), &pair); err != nil || pair.Generation != 2 || pair.AttachCredential == "old-attach" {
		t.Fatal(pair.Generation, err)
	}
	if w := call("GET", "agent-credentials", pair.Token); w.Code != 200 || !strings.Contains(w.Body.String(), pair.AttachCredential) {
		t.Fatal("current agent credential unavailable", w.Code)
	}
	if w := call("POST", "agent-rotation-ack", pair.Token); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := call("POST", "agent-rotation", "old"); w.Code != 401 {
		t.Fatal("acknowledged old proof still usable", w.Code)
	}
}
