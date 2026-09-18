package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

const e2eSessionGPU = "GPU-e2e-0000-0000-0000-000000000002"

// TestSessionRunnerCertifiesThroughItsUsageCallback proves a paid-session
// runner can be billed before the pool sells its work.
//
// A job runner reports usage in its response body, so certification can
// read it off the exchange it already made. A session runner reports
// asynchronously, to a callback the broker mints from its own
// external_base_url and hands over at create. That makes it a seam with
// three ways to fail silently — a base URL the runner cannot reach, a
// route that was never registered, a token that does not verify — and
// all three look identical from inside either service: the runner posts
// into nothing and the step fails the runner for saying nothing.
//
// So this test posts the usage report over real HTTP, to whatever URL
// the broker put in the create body.
func TestSessionRunnerCertifiesThroughItsUsageCallback(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two binaries")
	}
	p := startPool(t, fixtureCatalog(t))

	status, raw := p.controller(http.MethodPut, "/admin/v1/template-overrides/e2e-stream",
		`{"enabled":true}`)
	if status != http.StatusOK {
		t.Fatalf("enable session template: %d %s", status, raw)
	}

	m := signIn(t, p)
	h := m.enrol("rig-session")
	h.reportHardware(t, p, e2eSessionGPU, e2eGPUModel)

	status, raw = p.controller(http.MethodPost, "/admin/v1/reload", "")
	if status != http.StatusOK {
		t.Fatalf("reload: %d %s", status, raw)
	}

	runner := attach(t, p.brokerURL,
		sessionDocument(h.ID, h.AttachCredential, "e2e-stream", e2eSessionGPU), modeSession)
	runner.requireAccepted()

	// The broker certifies on match. A session recipe opens a session,
	// holds it, terminates, and then reads what the runner reported —
	// so reaching "advertised" means the callback worked.
	eventually(t, "the session offer to certify and advertise", 60*time.Second, func() error {
		status, raw := p.broker(http.MethodGet, "/admin/v1/offers/e2e-stream", "")
		if status != http.StatusOK {
			return fmt.Errorf("status %d: %s", status, raw)
		}
		var view struct {
			State      string `json:"state"`
			Advertised bool   `json:"advertised"`
		}
		decode(t, raw, &view)
		if !view.Advertised {
			// The runner listing names the rejection reason when a
			// capability was refused, which is the usual cause.
			_, runnerRaw := p.broker(http.MethodGet, "/admin/v1/runners", "")
			return fmt.Errorf("state=%s advertised=false; runners=%s", view.State, runnerRaw)
		}
		return nil
	})

	// The evidence, rather than the outcome alone: what the broker
	// handed the runner and how it answered the report.
	url, token, cbStatus := runner.callback()
	if url == "" || token == "" {
		t.Fatal("the broker opened a certification session without giving the runner anywhere " +
			"to report usage")
	}
	if !strings.HasPrefix(url, p.brokerURL) {
		t.Fatalf("the callback URL %q does not point at the broker; a runner elsewhere on the "+
			"network could not reach it", url)
	}
	if cbStatus != http.StatusOK {
		t.Fatalf("the broker answered the runner's usage report with %d", cbStatus)
	}

	// And the step's own verdict, with the units it read.
	status, raw = p.broker(http.MethodGet, "/admin/v1/certification", "")
	if status != http.StatusOK {
		t.Fatalf("certification list: %d %s", status, raw)
	}
	var runs struct {
		Runs []struct {
			OfferingID string `json:"offering_id"`
			State      string `json:"state"`
			Steps      []struct {
				Name     string         `json:"name"`
				Status   string         `json:"status"`
				Message  string         `json:"message"`
				Evidence map[string]any `json:"evidence"`
			} `json:"steps"`
		} `json:"results"`
	}
	decode(t, raw, &runs)
	var usage *struct {
		Name     string         `json:"name"`
		Status   string         `json:"status"`
		Message  string         `json:"message"`
		Evidence map[string]any `json:"evidence"`
	}
	for i := range runs.Runs {
		if runs.Runs[i].OfferingID != "e2e-stream" {
			continue
		}
		for j := range runs.Runs[i].Steps {
			if runs.Runs[i].Steps[j].Name == "usage" {
				usage = &runs.Runs[i].Steps[j]
			}
		}
	}
	if usage == nil {
		t.Fatalf("no usage step in the session certification run:\n%s", raw)
	}
	if usage.Status != "passed" {
		t.Fatalf("usage step = %s: %s", usage.Status, usage.Message)
	}
	if got := usage.Evidence["units"]; fmt.Sprint(got) != "5" {
		t.Fatalf("units = %v, want the 5 the runner reported", got)
	}
	if got := usage.Evidence["work_unit"]; got != "seconds" {
		t.Fatalf("work_unit = %v", got)
	}
}

// TestSessionRunnerThatCannotBeBilledIsRefused is the failure the tap
// was built to make possible.
//
// Before it existed the usage step recorded "not implemented", which is
// not a verdict — so a session runner that never reported usage was
// certified and its offering advertised. The pool would have sold work
// it could never settle, and nothing in the run would have said so.
func TestSessionRunnerThatCannotBeBilledIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two binaries")
	}
	p := startPool(t, fixtureCatalog(t))

	status, raw := p.controller(http.MethodPut, "/admin/v1/template-overrides/e2e-stream",
		`{"enabled":true}`)
	if status != http.StatusOK {
		t.Fatalf("enable session template: %d %s", status, raw)
	}
	m := signIn(t, p)
	h := m.enrol("rig-silent")
	h.reportHardware(t, p, e2eSessionGPU, e2eGPUModel)
	if status, raw := p.controller(http.MethodPost, "/admin/v1/reload", ""); status != http.StatusOK {
		t.Fatalf("reload: %d %s", status, raw)
	}

	// Same runner, one difference: it opens and terminates sessions
	// correctly but never reports what they used.
	runner := attach(t, p.brokerURL,
		sessionDocument(h.ID, h.AttachCredential, "e2e-stream", e2eSessionGPU), modeSilentSession)
	runner.requireAccepted()

	eventually(t, "the certification run to finish", 60*time.Second, func() error {
		_, raw := p.broker(http.MethodGet, "/admin/v1/certification", "")
		if !strings.Contains(string(raw), `"state":"failed"`) {
			return fmt.Errorf("no failed run yet: %s", raw)
		}
		return nil
	})

	status, raw = p.broker(http.MethodGet, "/admin/v1/offers/e2e-stream", "")
	if status != http.StatusOK {
		t.Fatalf("offer: %d %s", status, raw)
	}
	var view struct {
		Advertised bool `json:"advertised"`
		Runners    struct {
			Eligible int `json:"eligible"`
		} `json:"runners"`
	}
	decode(t, raw, &view)
	if view.Advertised || view.Runners.Eligible > 0 {
		t.Fatal("a session runner that reported no usage was made eligible; the pool would " +
			"sell work it cannot settle")
	}
	// The runner was given somewhere to report and simply did not.
	if url, _, _ := runner.callback(); url == "" {
		t.Fatal("this run proves nothing: the runner was never given a callback, so its " +
			"silence was not its own")
	}
}
