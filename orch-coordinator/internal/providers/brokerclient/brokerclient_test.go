package brokerclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPClient_FetchOfferings_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/registry/offerings" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"orch_eth_address":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","capabilities":[]}`))
	}))
	defer srv.Close()
	c := New(2 * time.Second)
	out, err := c.FetchOfferings(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if out.OrchEthAddress != "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("orch: %q", out.OrchEthAddress)
	}
}

func TestHTTPClient_FetchOfferings_5xxIsSoftFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := New(2 * time.Second)
	_, err := c.FetchOfferings(context.Background(), srv.URL)
	if !errors.Is(err, ErrBrokerUnreachable) {
		t.Fatalf("expected ErrBrokerUnreachable, got %v", err)
	}
}

func TestHTTPClient_FetchOfferings_4xxIsSchemaFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusForbidden)
	}))
	defer srv.Close()
	c := New(2 * time.Second)
	_, err := c.FetchOfferings(context.Background(), srv.URL)
	if !errors.Is(err, ErrBrokerSchema) {
		t.Fatalf("expected ErrBrokerSchema, got %v", err)
	}
}

func TestHTTPClient_FetchOfferings_MalformedBodyIsSchemaFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{not-json`))
	}))
	defer srv.Close()
	c := New(2 * time.Second)
	_, err := c.FetchOfferings(context.Background(), srv.URL)
	if !errors.Is(err, ErrBrokerSchema) {
		t.Fatalf("expected ErrBrokerSchema, got %v", err)
	}
}

func TestHTTPClient_FetchOfferings_TimeoutIsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer srv.Close()
	c := New(50 * time.Millisecond)
	_, err := c.FetchOfferings(context.Background(), srv.URL)
	if !errors.Is(err, ErrBrokerUnreachable) {
		t.Fatalf("expected ErrBrokerUnreachable, got %v", err)
	}
}

func TestHTTPClient_FetchOfferings_AppendsRegistryPath(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"orch_eth_address":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","capabilities":[]}`))
	}))
	defer srv.Close()
	c := New(2 * time.Second)
	if _, err := c.FetchOfferings(context.Background(), strings.TrimRight(srv.URL, "/")+"/"); err != nil {
		t.Fatal(err)
	}
	if seen != "/registry/offerings" {
		t.Fatalf("path: %q", seen)
	}
}

func TestHTTPClient_FetchHealth_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/registry/health" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"broker_status":"ready","generated_at":"2026-05-14T00:00:00Z","capabilities":[{"id":"cap","offering_id":"off","status":"ready"}]}`))
	}))
	defer srv.Close()
	c := New(2 * time.Second)
	out, err := c.FetchHealth(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if out.BrokerStatus != "ready" || len(out.Capabilities) != 1 {
		t.Fatalf("unexpected health %+v", out)
	}
}
