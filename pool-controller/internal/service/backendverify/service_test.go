package backendverify

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestVerifyJoinRequestPersistsResults(t *testing.T) {
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer probe.Close()

	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	if err := stateRepo.PutJoinRequest(types.JoinRequest{
		ID:               "join-1",
		MemberEthAddress: "0xabc",
		PayoutMode:       "onchain",
		RequestedBackends: []types.RequestedBackend{{
			ID:        "backend-1",
			Transport: "http",
			URL:       probe.URL + "/v1/rerank",
			HealthProbe: config.HealthProbe{
				Type:   "http-status",
				Config: map[string]any{"url": probe.URL + "/healthz"},
			},
		}},
	}); err != nil {
		t.Fatalf("PutJoinRequest() error = %v", err)
	}

	service := New(stateRepo)
	results, err := service.VerifyJoinRequest("join-1")
	if err != nil {
		t.Fatalf("VerifyJoinRequest() error = %v", err)
	}
	if len(results) != 1 || results[0].VerificationStatus != types.VerificationPassing {
		t.Fatalf("results = %#v", results)
	}
	updated, err := stateRepo.GetJoinRequest("join-1")
	if err != nil {
		t.Fatalf("GetJoinRequest() error = %v", err)
	}
	if updated.RequestedBackends[0].VerificationStatus != types.VerificationPassing || updated.RequestedBackends[0].LastVerifiedAt == nil {
		t.Fatalf("updated backend = %#v", updated.RequestedBackends[0])
	}
}

func TestVerifyMemberBackendPersistsFailure(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	if err := stateRepo.PutMemberBackend(types.MemberBackend{
		ID:        "backend-1",
		MemberID:  "member-1",
		Transport: "http",
		URL:       "http://127.0.0.1:1/v1/rerank",
		HealthProbe: config.HealthProbe{
			Type:   "http-status",
			Config: map[string]any{"url": "http://127.0.0.1:1/healthz"},
		},
	}); err != nil {
		t.Fatalf("PutMemberBackend() error = %v", err)
	}

	service := New(stateRepo)
	result, err := service.VerifyMemberBackend("backend-1")
	if err != nil {
		t.Fatalf("VerifyMemberBackend() error = %v", err)
	}
	if result.VerificationStatus != types.VerificationFailing || result.VerificationError == "" {
		t.Fatalf("result = %#v", result)
	}
	updated, err := stateRepo.GetMemberBackend("backend-1")
	if err != nil {
		t.Fatalf("GetMemberBackend() error = %v", err)
	}
	if updated.VerificationStatus != types.VerificationFailing || updated.LastVerifiedAt == nil {
		t.Fatalf("updated backend = %#v", updated)
	}
}
