package poolcontroller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRevenueStartRound(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       uint64
		bad        bool
	}{
		{"retired earliest", `{"pool_id":"p","sources":[{"source":{"pool_id":"p","source_id":"old"},"start_round":4341,"state":"retired"},{"source":{"pool_id":"p","source_id":"new"},"start_round":4400}]}`, 4341, false},
		{"empty", `{"pool_id":"p","sources":[]}`, 0, true},
		{"missing boundary", `{"pool_id":"p","sources":[{"source":{"pool_id":"p","source_id":"a"}}]}`, 0, true},
		{"wrong pool", `{"pool_id":"other","sources":[]}`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.RawQuery != "" {
					t.Error("boundary query must include all source history")
				}
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := &Client{baseURL: srv.URL, client: srv.Client()}
			got, err := c.RevenueStartRound(context.Background(), "p")
			if (err != nil) != tc.bad || got != tc.want {
				t.Fatalf("got %d, %v", got, err)
			}
		})
	}
}
