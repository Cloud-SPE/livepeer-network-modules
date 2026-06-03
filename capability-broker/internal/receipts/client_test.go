package receipts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
)

func TestHTTPClientUpsertWorkReceipt(t *testing.T) {
	var gotAuth string
	var gotReceipt WorkReceipt
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotReceipt); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client, err := NewHTTPClient(srv.URL, time.Second, config.AuthConfig{
		Method:    "bearer",
		SecretRef: "env://RECEIPT_SINK_TOKEN",
	}, backend.NewAuthApplier(backend.NewEnvSecretResolver()))
	if err != nil {
		t.Fatalf("NewHTTPClient() error = %v", err)
	}
	t.Setenv("RECEIPT_SINK_TOKEN", "top-secret")

	err = client.UpsertWorkReceipt(context.Background(), WorkReceipt{
		ID:                   "work-1",
		RequestID:            "req-1",
		CapabilityID:         "openai:chat-completions",
		OfferingID:           "shared",
		MemberEthAddress:     "0xabc",
		BackendID:            "backend-a",
		HostEnrollmentID:     "host-1",
		HardwareUnitID:       "gpu-1",
		GPUUUID:              "GPU-1",
		TemplateID:           "chat-4090",
		AcceptedWorkUnits:    42,
		AttributedRevenueWei: "1234",
		Status:               "stub",
	})
	if err != nil {
		t.Fatalf("UpsertWorkReceipt() error = %v", err)
	}
	if gotAuth != "Bearer top-secret" {
		t.Fatalf("Authorization = %q, want Bearer top-secret", gotAuth)
	}
	if gotReceipt.ID != "work-1" || gotReceipt.Status != "stub" {
		t.Fatalf("receipt = %#v", gotReceipt)
	}
	if gotReceipt.HostEnrollmentID != "host-1" || gotReceipt.HardwareUnitID != "gpu-1" || gotReceipt.TemplateID != "chat-4090" || gotReceipt.AttributedRevenueWei != "1234" {
		t.Fatalf("pool attribution fields were not preserved: %#v", gotReceipt)
	}
}
