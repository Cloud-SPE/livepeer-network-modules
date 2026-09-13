package poolcontroller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/config"
)

func TestLeaseAndAlertEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/admin/v1/payout-intents/claim", "/admin/v1/payout-intents/renew", "/admin/v1/payout-intents/release":
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("unexpected lease request %s %s", r.Method, r.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
			_, _ = io.WriteString(w, `{"lease_id":"lease-1","intents":[{"id":"p1"}]}`)
		case "/admin/v1/payout-intents/requeue":
			_, _ = io.WriteString(w, `{"intents":[{"id":"p2","status":"exported"}]}`)
		case "/admin/v1/payout-alerts":
			for _, key := range []string{"round_id", "member_eth_address", "status", "limit", "submitted_older_than_seconds", "failed_older_than_seconds", "lease_expires_within_seconds", "retry_count_at_least", "recent_requeue_within_seconds"} {
				if r.URL.Query().Get(key) == "" {
					t.Errorf("missing query %s in %s", key, r.URL.RawQuery)
				}
			}
			_, _ = io.WriteString(w, `{"summary":{"alert_count":1,"critical_count":1},"alerts":[{"type":"stale","severity":"critical","intent":{"id":"p3"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c, err := NewClient(config.PoolController{URL: server.URL + "/", BearerToken: " secret ", TimeoutMS: 500})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	lease, intents, err := c.ClaimPayoutIntents(ctx, ClaimPayoutIntentsRequest{ExecutorID: "e", LeaseTTLSeconds: 30, Limit: 1})
	if err != nil || lease != "lease-1" || len(intents) != 1 {
		t.Fatalf("claim lease=%q intents=%+v err=%v", lease, intents, err)
	}
	lease, intents, err = c.RenewPayoutIntents(ctx, RenewPayoutIntentsRequest{ExecutorID: "e", LeaseID: lease, LeaseTTLSeconds: 30})
	if err != nil || lease != "lease-1" || len(intents) != 1 {
		t.Fatalf("renew lease=%q intents=%+v err=%v", lease, intents, err)
	}
	lease, intents, err = c.ReleasePayoutIntents(ctx, ReleasePayoutIntentsRequest{ExecutorID: "e", LeaseID: lease, IDs: []string{"p1"}})
	if err != nil || lease != "lease-1" || len(intents) != 1 {
		t.Fatalf("release lease=%q intents=%+v err=%v", lease, intents, err)
	}
	requeued, err := c.RequeuePayoutIntents(ctx, RequeuePayoutIntentsRequest{IDs: []string{"p2"}})
	if err != nil || len(requeued) != 1 || requeued[0].Status != "exported" {
		t.Fatalf("requeue=%+v err=%v", requeued, err)
	}
	summary, alerts, err := c.ListPayoutAlerts(ctx, ListPayoutAlertsOptions{
		RoundID: "9", MemberEthAddress: "0xabc", Status: "submitted", Limit: 10,
		SubmittedOlderThanSeconds: 1, FailedOlderThanSeconds: 2,
		LeaseExpiresWithinSeconds: 3, RetryCountAtLeast: 4, RecentRequeueWithinSeconds: 5,
	})
	if err != nil || summary.AlertCount != 1 || len(alerts) != 1 || alerts[0].Intent.ID != "p3" {
		t.Fatalf("alerts summary=%+v alerts=%+v err=%v", summary, alerts, err)
	}
}

func TestClientEndpointFailures(t *testing.T) {
	for _, response := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "status", status: http.StatusBadGateway, body: "upstream failed"},
		{name: "decode", status: http.StatusOK, body: "{"},
	} {
		t.Run(response.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(response.status)
				_, _ = io.WriteString(w, response.body)
			}))
			defer s.Close()
			c, _ := NewClient(config.PoolController{URL: s.URL})
			ctx := context.Background()
			calls := []func() error{
				func() error { _, _, err := c.ClaimPayoutIntents(ctx, ClaimPayoutIntentsRequest{}); return err },
				func() error { _, _, err := c.RenewPayoutIntents(ctx, RenewPayoutIntentsRequest{}); return err },
				func() error { _, _, err := c.ReleasePayoutIntents(ctx, ReleasePayoutIntentsRequest{}); return err },
				func() error { _, err := c.RequeuePayoutIntents(ctx, RequeuePayoutIntentsRequest{}); return err },
				func() error { _, err := c.ListPayoutIntents(ctx, ListPayoutIntentsOptions{}); return err },
				func() error { _, _, err := c.ListPayoutAlerts(ctx, ListPayoutAlertsOptions{}); return err },
				func() error { _, err := c.UpdatePayoutIntentStatus(ctx, UpdatePayoutIntentStatusRequest{}); return err },
			}
			for i, call := range calls {
				if err := call(); err == nil {
					t.Fatalf("call %d unexpectedly succeeded", i)
				} else if response.name == "status" && !strings.Contains(err.Error(), "status 502") {
					t.Fatalf("call %d error=%v", i, err)
				}
			}
		})
	}
}

func TestNewClientTokenResolution(t *testing.T) {
	t.Setenv("PAYOUT_TEST_TOKEN", "from-env")
	c, err := NewClient(config.PoolController{URL: "http://example.test", BearerTokenRef: "env://PAYOUT_TEST_TOKEN"})
	if err != nil || c.token != "from-env" || c.client.Timeout == 0 {
		t.Fatalf("client=%+v err=%v", c, err)
	}
	if _, err := NewClient(config.PoolController{URL: "http://%"}); err == nil {
		t.Fatal("invalid URL accepted")
	}
	if _, err := resolveBearerToken(config.PoolController{BearerTokenRef: "env://"}); err == nil {
		t.Fatal("empty token reference accepted")
	}
	os.Unsetenv("PAYOUT_MISSING_TOKEN")
	if _, err := resolveBearerToken(config.PoolController{BearerTokenRef: "env://PAYOUT_MISSING_TOKEN"}); err == nil {
		t.Fatal("missing token accepted")
	}
	t.Setenv("PAYOUT_EMPTY_TOKEN", "")
	if _, err := resolveBearerToken(config.PoolController{BearerTokenRef: "env://PAYOUT_EMPTY_TOKEN"}); err == nil {
		t.Fatal("empty token accepted")
	}
}
