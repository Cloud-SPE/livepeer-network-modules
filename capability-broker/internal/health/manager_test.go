package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
)

func TestManagerHTTPStatusProbeBecomesReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mgr := New(&config.Config{
		Capabilities: []config.Capability{{
			ID:         "demo:echo:v1",
			OfferingID: "default",
			Health: config.Health{
				InitialStatus: "stale",
				Probe: config.HealthProbe{
					Type:           "http-status",
					IntervalMS:     10,
					TimeoutMS:      50,
					HealthyAfter:   1,
					UnhealthyAfter: 1,
					Config: map[string]any{
						"url": srv.URL,
					},
				},
			},
		}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Run(ctx)
	time.Sleep(40 * time.Millisecond)

	snap := mgr.Snapshot()
	if len(snap.Capabilities) != 1 {
		t.Fatalf("capability count = %d, want 1", len(snap.Capabilities))
	}
	if got := snap.Capabilities[0].Status; got != StatusReady {
		t.Fatalf("status = %q, want %q", got, StatusReady)
	}
}

func TestManagerManualDrainStaysDraining(t *testing.T) {
	mgr := New(&config.Config{
		Capabilities: []config.Capability{{
			ID:         "video:live.rtmp",
			OfferingID: "default",
			Health: config.Health{
				InitialStatus: "stale",
				Drain:         config.HealthDrain{Enabled: true},
				Probe:         config.HealthProbe{Type: "manual-drain"},
			},
		}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Run(ctx)
	time.Sleep(10 * time.Millisecond)

	snap := mgr.Snapshot()
	if got := snap.Capabilities[0].Status; got != StatusDraining {
		t.Fatalf("status = %q, want %q", got, StatusDraining)
	}
}
