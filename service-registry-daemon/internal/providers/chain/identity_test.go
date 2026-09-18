package chain

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateAllRPCChainIDs(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0xa4b1"}`))
	}))
	defer good.Close()
	wrong := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}))
	defer wrong.Close()
	for _, tc := range []struct {
		name    string
		urls    []string
		id      int64
		wantErr bool
	}{
		{"matching", []string{good.URL}, 42161, false},
		{"wrong backup", []string{good.URL, wrong.URL}, 42161, true},
		{"empty", nil, 42161, true},
		{"invalid id", []string{good.URL}, 0, true},
		{"bad URL", []string{"://"}, 42161, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateRPCChainIDs(context.Background(), tc.urls, tc.id); (err != nil) != tc.wantErr {
				t.Fatalf("got %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ValidateRPCChainIDs(ctx, []string{good.URL}, 42161); err == nil {
		t.Fatal("expected cancellation")
	}
}
