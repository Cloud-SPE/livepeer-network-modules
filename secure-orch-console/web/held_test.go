package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/agent"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/audit"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/lastsigned"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/policy"
)

func heldTestManifest(t *testing.T, addr string, seq uint64, price string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"spec_version":    "0.2.0",
		"publication_seq": seq,
		"issued_at":       "2026-06-11T00:00:00Z",
		"expires_at":      "2026-06-12T00:00:00Z",
		"orch":            map[string]any{"eth_address": addr},
		"capabilities": []any{map[string]any{
			"capability_id":      "openai:chat",
			"offering_id":        "small",
			"interaction_mode":   "http-stream@v1",
			"work_unit":          map[string]any{"name": "tokens"},
			"price_per_unit_wei": price,
			"worker_url":         "https://a.workers.example/",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestServer_HeldQueueApproveFlow(t *testing.T) {
	heldDir := t.TempDir()
	srv, _, cleanup := newHarnessWithConfig(t, config.Config{Listen: "127.0.0.1:0", AgentHeldDir: heldDir})
	defer cleanup()

	addr := strings.ToLower(srv.signer.Address().String())

	// Last-signed at seq 9; the held candidate carries a stale seq 6,
	// so loading it must apply sequence discipline → 10.
	lastEnvelope, err := json.Marshal(map[string]any{
		"manifest":  json.RawMessage(heldTestManifest(t, addr, 9, "1000")),
		"signature": map[string]any{"algorithm": "secp256k1", "value": "0xdead", "canonicalization": "JCS"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := lastsigned.WriteAtomic(srv.cfg.LastSignedPath, lastEnvelope); err != nil {
		t.Fatal(err)
	}

	held := &agent.HeldQueue{Dir: heldDir}
	if _, err := held.Put(agent.HeldItem{
		ETag:            `"etag-1"`,
		HeldAt:          time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC),
		PublicationSeq:  6,
		CanonicalSHA256: "abc123",
		Class:           "benign",
		ShadowAutoSign:  true,
		Findings: []policy.Finding{{
			ClassName: "benign", Code: "price_change_within_bound",
			CapabilityID: "openai:chat", OfferingID: "small",
			Detail: "price 1000 → 1050 (bound ±10%)",
		}},
	}, heldTestManifest(t, addr, 6, "1050")); err != nil {
		t.Fatal(err)
	}

	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	if err := waitFor("http://" + srv.Addr() + "/healthz"); err != nil {
		t.Fatal(err)
	}
	url := "http://" + srv.Addr()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// The manifests page surfaces the pending card.
	resp, err := http.Get(url + "/manifests")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"Pending changes held by the agent", "benign", "price_change_within_bound", "/held/load", "shadow mode"} {
		if !strings.Contains(string(page), want) {
			t.Fatalf("manifests page missing %q", want)
		}
	}

	// Load for review: stashes the candidate with seq discipline.
	resp, err = noRedirect.Post(url+"/held/load", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("held/load status=%d want 303", resp.StatusCode)
	}
	resp, err = http.Get(url + "/manifests")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "agent-held") {
		t.Fatalf("candidate source missing agent-held marker")
	}
	if !strings.Contains(string(page), "<dd>10</dd>") {
		t.Fatalf("expected seq discipline 9+1=10 on review page")
	}

	// Approve: confirm gesture signs, clears the held slot, audits
	// operator_approve, and redirects — no download attachment (the
	// agent pushes).
	form := strings.NewReader("confirm_last4=" + lastFourHex(addr))
	resp, err = noRedirect.Post(url+"/sign", "application/x-www-form-urlencoded", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("approve sign status=%d want 303 redirect", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Disposition"); got != "" {
		t.Fatalf("approve must not serve a download, got %q", got)
	}

	item, _, err := held.Current()
	if err != nil {
		t.Fatal(err)
	}
	if item != nil {
		t.Fatal("held slot must clear after operator approval")
	}
	persisted, err := lastsigned.Load(srv.cfg.LastSignedPath)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Manifest struct {
			PublicationSeq uint64 `json:"publication_seq"`
		} `json:"manifest"`
		Signature struct {
			Value string `json:"value"`
		} `json:"signature"`
	}
	if err := json.Unmarshal(persisted, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Manifest.PublicationSeq != 10 {
		t.Fatalf("signed seq=%d want 10", envelope.Manifest.PublicationSeq)
	}
	if len(envelope.Signature.Value) < 10 {
		t.Fatal("signed envelope missing real signature")
	}

	events, err := audit.ReadRecent(filepath.Join(srv.cfg.AuditLogPath), 50)
	if err != nil {
		t.Fatal(err)
	}
	approved := false
	for _, ev := range events {
		if ev.Kind == audit.KindOperatorApprove {
			approved = true
			if ev.Fields["etag"] != `"etag-1"` {
				t.Fatalf("operator_approve etag=%v", ev.Fields["etag"])
			}
		}
	}
	if !approved {
		t.Fatalf("missing operator_approve audit event")
	}
}

func TestServer_HeldLoadWithoutQueueIs404(t *testing.T) {
	srv, _, cleanup := newHarness(t, "127.0.0.1:0")
	defer cleanup()
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	if err := waitFor("http://" + srv.Addr() + "/healthz"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post("http://"+srv.Addr()+"/held/load", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want 404", resp.StatusCode)
	}
}
