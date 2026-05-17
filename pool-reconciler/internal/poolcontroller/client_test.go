package poolcontroller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/types"
)

func TestSubmitRoundClose(t *testing.T) {
	var gotAuth string
	var gotReq types.RoundCloseRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("POOL_CONTROLLER_ADMIN_TOKEN", "secret")
	client, err := NewClient(config.PoolController{
		URL:            srv.URL,
		BearerTokenRef: "env://POOL_CONTROLLER_ADMIN_TOKEN",
		TimeoutMS:      1000,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	err = client.SubmitRoundClose(context.Background(), types.RoundCloseRequest{
		ID:                     "close-1",
		RoundID:                "124",
		PoolRevenueWei:         "2000",
		PoolCutWei:             "200",
		IncludedWorkReceiptIDs: []string{"work-1"},
	})
	if err != nil {
		t.Fatalf("SubmitRoundClose() error = %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("Authorization = %q, want Bearer secret", gotAuth)
	}
	if gotReq.ID != "close-1" || gotReq.RoundID != "124" {
		t.Fatalf("request = %#v", gotReq)
	}
}
