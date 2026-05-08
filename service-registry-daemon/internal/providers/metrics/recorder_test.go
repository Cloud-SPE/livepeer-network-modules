package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNoop_AllMethodsAreSafe(t *testing.T) {
	r := NewNoop()
	r.IncGRPCRequest("a", "b", "c", "d")
	r.ObserveGRPC("a", "b", time.Millisecond)
	r.SetGRPCInFlight("a", "b", 1)
	r.IncResolution("well_known", "fresh")
	r.ObserveResolveDuration("well_known", "fresh", time.Millisecond)
	r.IncLegacyFallback("manifest_unavailable")
	r.IncManifestFetch("ok")
	r.ObserveManifestFetch("ok", time.Millisecond, 1024)
	r.IncManifestVerify("verified")
	r.ObserveSignatureVerify(time.Microsecond)
	r.IncCacheLookup("hit_fresh")
	r.IncCacheWrite()
	r.IncCacheEviction("max_stale")
	r.SetCacheEntries(5)
	r.IncAudit("manifest_fetched")
	r.IncOverlayReload("ok")
	r.SetOverlayEntries(2)
	r.IncOverlayDrop("disabled")
	r.IncChainRead("ok")
	r.IncChainWrite("ok")
	r.ObserveChainRead(time.Millisecond)
	r.SetChainLastSuccess(time.Now())
	r.SetManifestFetcherLastSuccess(time.Now())
	r.IncPublisherBuild()
	r.IncPublisherSign("ok")
	r.IncPublisherProbe("ok")
	r.SetUptimeSeconds(10)
	r.SetBuildInfo("v0.0.0", "test", "go1.25")
}

func TestNoop_HandlerReturns404(t *testing.T) {
	srv := httptest.NewServer(NewNoop().Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestPrometheus_HandlerExposesNamespace(t *testing.T) {
	r := NewPrometheus(PrometheusConfig{MaxSeriesPerMetric: 100})
	r.IncGRPCRequest("Resolver", "ResolveByAddress", "OK", "")
	r.IncResolution("well_known", "fresh")
	r.IncManifestVerify("verified")
	r.IncCacheLookup("hit_fresh")
	r.SetBuildInfo("v0.0.0", "test", "go1.25")

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	body := readAll(t, resp)
	mustContain(t, body, "livepeer_registry_grpc_requests_total")
	mustContain(t, body, `service="Resolver"`)
	mustContain(t, body, `method="ResolveByAddress"`)
	mustContain(t, body, "livepeer_registry_resolutions_total")
	mustContain(t, body, "livepeer_registry_manifest_verifications_total")
	mustContain(t, body, "livepeer_registry_cache_lookups_total")
	mustContain(t, body, "livepeer_registry_build_info")
	// Standard collectors
	mustContain(t, body, "go_goroutines")
	mustContain(t, body, "process_cpu_seconds_total")
}

func TestPrometheus_EmptyLabelBecomesUnset(t *testing.T) {
	r := NewPrometheus(PrometheusConfig{})
	r.IncGRPCRequest("svc", "m", "OK", "") // empty registry_code
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	mustContain(t, body, `registry_code="`+LabelUnset+`"`)
}

func TestPrometheus_CardinalityCapEnforced(t *testing.T) {
	var capHits []string
	r := NewPrometheus(PrometheusConfig{
		MaxSeriesPerMetric: 2,
		OnCapExceeded: func(metricName string, observed, cap int) {
			capHits = append(capHits, metricName)
		},
	})
	// Two distinct values: pass.
	r.IncLegacyFallback("a")
	r.IncLegacyFallback("b")
	// Third distinct value: capped.
	r.IncLegacyFallback("c")
	// Re-using an existing label still works.
	r.IncLegacyFallback("a")
	r.IncLegacyFallback("a")

	if len(capHits) != 1 || !strings.Contains(capHits[0], "legacy_fallbacks_total") {
		t.Fatalf("expected one cap-hit notification for legacy_fallbacks_total, got %v", capHits)
	}

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	// "a" and "b" should be present, "c" should not.
	mustContain(t, body, `reason="a"`)
	mustContain(t, body, `reason="b"`)
	if strings.Contains(body, `reason="c"`) {
		t.Fatalf("expected reason=c to be dropped:\n%s", body)
	}
}

func TestPrometheus_CapDisabled(t *testing.T) {
	r := NewPrometheus(PrometheusConfig{MaxSeriesPerMetric: 0})
	for i := 0; i < 50; i++ {
		r.IncLegacyFallback(string(rune('a' + i%26)))
	}
	// No assertion — just confirms zero cap doesn't panic and labels
	// are accepted freely.
}

func TestPrometheus_HistogramsRecord(t *testing.T) {
	r := NewPrometheus(PrometheusConfig{})
	r.ObserveGRPC("Resolver", "ResolveByAddress", 500*time.Microsecond)
	r.ObserveResolveDuration("well_known", "fresh", 5*time.Millisecond)
	r.ObserveManifestFetch("ok", 50*time.Millisecond, 4096)
	r.ObserveSignatureVerify(2 * time.Millisecond)
	r.ObserveChainRead(20 * time.Millisecond)

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	mustContain(t, body, "livepeer_registry_grpc_request_duration_seconds_bucket")
	mustContain(t, body, "livepeer_registry_grpc_request_duration_seconds_fast_bucket")
	mustContain(t, body, "livepeer_registry_resolve_duration_seconds_bucket")
	mustContain(t, body, "livepeer_registry_manifest_fetch_duration_seconds_bucket")
	mustContain(t, body, "livepeer_registry_manifest_fetch_bytes_bucket")
	mustContain(t, body, "livepeer_registry_signature_verify_duration_seconds_bucket")
	mustContain(t, body, "livepeer_registry_chain_read_duration_seconds_bucket")
}

func TestPrometheus_GaugesSet(t *testing.T) {
	r := NewPrometheus(PrometheusConfig{})
	r.SetCacheEntries(7)
	r.SetOverlayEntries(3)
	r.SetChainLastSuccess(time.Unix(1745000000, 0))
	r.SetManifestFetcherLastSuccess(time.Unix(1745000060, 0))
	r.SetGRPCInFlight("svc", "m", 4)
	r.SetUptimeSeconds(123.4)

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	mustContain(t, body, "livepeer_registry_cache_entries 7")
	mustContain(t, body, "livepeer_registry_overlay_entries 3")
	mustContain(t, body, "livepeer_registry_chain_last_success_timestamp_seconds 1.745e+09")
	mustContain(t, body, `livepeer_registry_grpc_in_flight_requests{method="m",service="svc"} 4`)
	mustContain(t, body, "livepeer_registry_uptime_seconds 123.4")
}

func TestUnsetEmptyString(t *testing.T) {
	if got := unset(""); got != LabelUnset {
		t.Fatalf("got %q", got)
	}
	if got := unset("ok"); got != "ok" {
		t.Fatalf("got %q", got)
	}
}

func TestJoinNul(t *testing.T) {
	a := joinNul([]string{"a", "b"})
	b := joinNul([]string{"ab", ""})
	if a == b {
		t.Fatalf("nul-separator collision: a=%q b=%q", a, b)
	}
}

// --- helpers ---

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	buf := make([]byte, 0, 8192)
	tmp := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return string(buf)
}

func mustContain(t *testing.T, body, sub string) {
	t.Helper()
	if !strings.Contains(body, sub) {
		t.Fatalf("body missing %q. body:\n%s", sub, body)
	}
}
