package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/poolcontroller"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/types"
)

func scopedFixture(t *testing.T, pool, resource, role string, handler http.HandlerFunc) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	token, digest, err := serviceauth.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	auth := filepath.Join(dir, "auth.json")
	tokenFile := filepath.Join(dir, "token")
	ca := filepath.Join(dir, "ca.pem")
	raw, _ := json.Marshal(serviceauth.File{Credentials: []serviceauth.Credential{{ID: "test-reader", PoolID: pool, Roles: []string{role}, Resources: []string{resource}, ExpiresAt: time.Now().Add(time.Hour), TokenSHA256: digest}}})
	if err = os.WriteFile(auth, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := (serviceauth.Verifier{Path: auth}).Authorize(r, pool, resource, role); err != nil {
			http.Error(w, "scope rejected", 401)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	return srv.URL, tokenFile, ca
}
func TestRegionalCollectionIndependentPoolsAllSourcesAndAllReceiptPages(t *testing.T) {
	for _, topology := range []struct {
		pool  string
		count int
	}{{"pool_us", 3}, {"pool_eu", 1}} {
		t.Run(topology.pool, func(t *testing.T) {
			var mode atomic.Value
			mode.Store("")
			cfg := &config.Config{PoolController: config.PoolController{PoolID: topology.pool}}
			receipts := []poolcontroller.WorkReceipt{}
			names := []string{"transcode-broker", "audio-broker", "llm-broker"}
			for i := 0; i < topology.count; i++ {
				source := revenue.Source{PoolID: topology.pool, SourceID: "0x" + strings.Repeat(strconv.Itoa(i+1), 64), BrokerID: names[i], ChainID: 42161, Payee: "0x" + strings.Repeat("a", 40)}
				entries := []revenue.Contribution{}
				count := 0
				if i == 0 {
					count = 601
				}
				for j := 0; j < count; j++ {
					id := source.SourceID + fmt.Sprintf("/job-%04d", j)
					receipts = append(receipts, poolcontroller.WorkReceipt{PoolID: topology.pool, SourceID: source.SourceID, ID: id, RoundID: "100", MemberEthAddress: "member", BackendID: "host|gpu", OfferingID: "offer", AttributedRevenueWei: "1", Status: "final"})
					entries = append(entries, revenue.Contribution{ID: id, PoolID: topology.pool, SourceID: source.SourceID, RoundID: "100", Member: "member", Backend: "host|gpu", Offering: "offer", AmountWei: "1"})
				}
				digest, _ := revenue.WorkDigest(entries)
				source.URL, source.TokenFile, source.CAFile = scopedFixture(t, topology.pool, source.BrokerID, "revenue-reader", func(w http.ResponseWriter, r *http.Request) {
					current := mode.Load().(string)
					if i == topology.count-1 && current == "offline" {
						http.Error(w, "receiver unavailable", 503)
						return
					}
					if strings.Contains(r.URL.Path, "/work/") {
						json.NewEncoder(w).Encode(revenue.WorkReport{PoolID: source.PoolID, SourceID: source.SourceID, BrokerID: source.BrokerID, Round: 100, Complete: current != "incomplete-work", ClosedThroughRound: 100, ObservedAt: time.Now(), ReceiptCount: uint64(count), ReceiptDigest: digest})
						return
					}
					report := revenue.Report{PoolID: source.PoolID, SourceID: source.SourceID, BrokerID: source.BrokerID, ChainID: source.ChainID, Payee: source.Payee, Round: 100, RevenueWei: "100", TicketCount: 601, Complete: true, CompleteThroughRound: 100, ObservedAt: time.Now(), ObservedHead: 1000, FinalizedBlock: 996, ObservedHeadHash: "0x" + strings.Repeat("b", 64), FinalizedBlockHash: "0x" + strings.Repeat("c", 64), InclusionDigest: strings.Repeat("d", 64)}
					if i == topology.count-1 {
						switch current {
						case "wrong-pool":
							report.PoolID = "foreign"
						case "wrong-ledger":
							report.SourceID = "0x" + strings.Repeat("f", 64)
						case "stale":
							report.ObservedAt = time.Now().Add(-time.Hour)
						case "incomplete":
							report.Complete = false
						}
					}
					json.NewEncoder(w).Encode(report)
				})
				cfg.RevenueSources = append(cfg.RevenueSources, source)
			}
			pages := atomic.Int64{}
			cfg.PoolController.URL, cfg.PoolController.TokenFile, cfg.PoolController.CAFile = scopedFixture(t, topology.pool, "controller", "reconciler", func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/revenue-sources") {
					rows := []map[string]any{}
					for _, source := range cfg.RevenueSources {
						rows = append(rows, map[string]any{"source": source})
					}
					json.NewEncoder(w).Encode(map[string]any{"pool_id": topology.pool, "sources": rows})
					return
				}
				pages.Add(1)
				if r.URL.Query().Get("round_id") != "100" || r.URL.Query().Get("status") != "final" || r.URL.Query().Get("pagination") != "true" {
					http.Error(w, "missing filters", 400)
					return
				}
				start := 0
				if r.URL.Query().Get("cursor") != "" {
					start = 500
				}
				limit := len(receipts)
				if mode.Load().(string) == "missing-receipt" {
					limit--
				}
				end := start + 500
				if end > limit {
					end = limit
				}
				cursor := ""
				if end < limit {
					cursor = receipts[end-1].ID
				}
				snapshot := "snapshot"
				if start > 0 && mode.Load().(string) == "changed-snapshot" {
					snapshot = "changed"
				}
				json.NewEncoder(w).Encode(map[string]any{"receipts": receipts[start:end], "total": limit, "next_cursor": cursor, "snapshot": snapshot})
			})
			client, err := poolcontroller.NewClient(cfg.PoolController)
			if err != nil {
				t.Fatal(err)
			}
			initial := types.RoundCloseRequest{ID: "regional-close-100", RoundID: "100"}
			result, err := prepareRegionalClose(context.Background(), cfg, client, initial)
			if err != nil || result.PoolRevenueWei != strconv.Itoa(topology.count*100) || len(result.RevenueReports) != topology.count || len(result.IncludedWorkReceiptIDs) != 601 || pages.Load() != 2 {
				t.Fatalf("collection revenue=%s sources=%d receipts=%d pages=%d err=%v", result.PoolRevenueWei, len(result.RevenueReports), len(result.IncludedWorkReceiptIDs), pages.Load(), err)
			}
			// Reusing a prepared request must not append duplicate source proofs.
			result, err = prepareRegionalClose(context.Background(), cfg, client, result)
			if err != nil || len(result.WorkReports) != topology.count {
				t.Fatalf("retry %+v %v", result, err)
			}
			for _, failure := range []string{"offline", "wrong-pool", "wrong-ledger", "stale", "incomplete", "incomplete-work", "missing-receipt", "changed-snapshot"} {
				mode.Store(failure)
				if _, err = prepareRegionalClose(context.Background(), cfg, client, initial); err == nil {
					t.Fatalf("%s treated as complete", failure)
				}
			}
			mode.Store("")
			path := filepath.Join(t.TempDir(), "reconcile.db")
			st, err := repo.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = st.SavePrepared(100, result); err != nil {
				t.Fatal(err)
			}
			st.Close()
			st, err = repo.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			saved, found, err := st.GetRound(100)
			if err != nil || !found || saved.Prepared == nil || len(saved.Prepared.RevenueReports) != topology.count || len(saved.Prepared.IncludedWorkReceiptIDs) != 601 {
				t.Fatalf("lost prepared evidence %+v %v", saved, err)
			}
		})
	}
}

func TestLostCloseAcknowledgementReplaysPersistedProofAfterSourceRetirement(t *testing.T) {
	cfg := &config.Config{PoolController: config.PoolController{PoolID: "pool_us"}}
	calls := 0
	cfg.PoolController.URL, cfg.PoolController.TokenFile, cfg.PoolController.CAFile = scopedFixture(t, "pool_us", "controller", "reconciler", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/admin/v1/round-close" {
			t.Errorf("recovery contacted retired source registry: %s", r.URL.Path)
			http.Error(w, "unavailable", 503)
			return
		}
		calls++
		var req types.RoundCloseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID != "close-100" || req.PoolRevenueWei != "300" {
			t.Errorf("changed retry %+v %v", req, err)
		}
		w.WriteHeader(200)
	})
	path := filepath.Join(t.TempDir(), "reconcile.db")
	st, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	request := types.RoundCloseRequest{ID: "close-100", PoolID: "pool_us", RoundID: "100", PoolRevenueWei: "300", PoolCutWei: "0", ReceiptSnapshot: "original"}
	if err = st.SavePrepared(100, request); err != nil {
		t.Fatal(err)
	}
	if err = st.MarkFailed(100, "controller response lost"); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	client, err := poolcontroller.NewClient(cfg.PoolController)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = attemptRoundClose(context.Background(), cfg, client, st, 100); err != nil {
		t.Fatal(err)
	}
	record, _, err := st.GetRound(100)
	if err != nil || record.Status != "closed" || calls != 1 {
		t.Fatalf("lost acknowledgement recovery %+v %v calls=%d", record, err, calls)
	}
}
