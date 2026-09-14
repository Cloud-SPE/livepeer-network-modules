package livehealthfetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInvalidResponseAndCanceledFetch(t *testing.T) {
	f := New(0)
	if _, err := f.Fetch(context.Background(), ":bad"); err == nil {
		t.Fatal("invalid URL accepted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) }))
	defer server.Close()
	if _, err := f.Fetch(context.Background(), server.URL); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Fetch(ctx, server.URL); err == nil {
		t.Fatal("canceled request succeeded")
	}
}
