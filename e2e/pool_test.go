package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	e2eGPUUUID  = "GPU-e2e-0000-0000-0000-000000000001"
	e2eGPUModel = "NVIDIA GeForce RTX 4090"
)

// TestPoolOnboardsAMemberEndToEnd walks one member from signing in to
// being certified and earning, across two real processes.
//
// Every step here has a unit test somewhere. What no unit test covers
// is that they line up: the defects this suite exists for were all of
// the shape "both sides are individually correct and disagree about the
// wire" — an attach field the broker's allowlist did not carry, a
// desired-state document missing what the agent needs to declare, a
// bundle whose variable names the agent does not read.
func TestPoolOnboardsAMemberEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two binaries")
	}
	p := startPool(t, fixtureCatalog(t))

	// The operator's whole gesture: enable a template and price it.
	status, raw := p.controller(http.MethodPut, "/admin/v1/template-overrides/e2e-chat",
		`{"enabled":true,"price":{"amount_wei":"2000000000","per_units":1000}}`)
	if status != http.StatusOK {
		t.Fatalf("enable template: %d %s", status, raw)
	}

	// A member signs in with their wallet and enrols a host.
	m := signIn(t, p)
	h := m.enrol("rig-1")
	h.reportHardware(t, p, e2eGPUUUID, e2eGPUModel)

	// The controller owns the offer set and the credentials; the broker
	// receives both. Nothing else gives the host permission to attach.
	status, raw = p.controller(http.MethodPost, "/admin/v1/reload", "")
	if status != http.StatusOK {
		t.Fatalf("reload: %d %s", status, raw)
	}

	var offers struct {
		Offers []struct {
			OfferingID string `json:"offering_id"`
			Operator   struct {
				Price struct {
					AmountWei string `json:"amount_wei"`
				} `json:"price"`
			} `json:"operator"`
		} `json:"offers"`
	}
	status, raw = p.broker(http.MethodGet, "/admin/v1/offers", "")
	if status != http.StatusOK {
		t.Fatalf("broker offers: %d %s", status, raw)
	}
	decode(t, raw, &offers)
	if len(offers.Offers) != 1 {
		t.Fatalf("the broker should hold exactly the one offer the operator enabled, got %d: %s",
			len(offers.Offers), raw)
	}
	// The pool's price, not the catalog's suggestion. A push that
	// dropped the override would still look like a working push.
	if got := offers.Offers[0].Operator.Price.AmountWei; got != "2000000000" {
		t.Fatalf("the broker is selling at the catalog's default price, not the pool's: %q", got)
	}

	// The host attaches. This document is the member's agent speaking,
	// authenticated by the credential the controller pushed.
	runner := attach(t, p.brokerURL, hostDocument(
		h.ID, h.AttachCredential, "e2e-chat", "openai:chat-completions", "e2e-model", e2eGPUUUID), modeJob)
	runner.requireAccepted()
	if got := runner.acceptedLocalIDs(); len(got) != 1 {
		t.Fatalf("expected the one capability to be accepted, got %v", got)
	}

	// The broker now knows hardware the controller only heard about
	// second-hand. The relay loop reconciles the two.
	eventually(t, "the broker to list the attached runner", 30*time.Second, func() error {
		status, raw := p.broker(http.MethodGet, "/admin/v1/runners", "")
		if status != http.StatusOK {
			return fmt.Errorf("status %d: %s", status, raw)
		}
		if !strings.Contains(string(raw), h.ID) {
			return fmt.Errorf("host %s absent from %s", h.ID, raw)
		}
		return nil
	})

	// Placement decides what this card should run. With one template
	// and one qualifying card there is exactly one answer, and the
	// operator applies it.
	status, raw = p.controller(http.MethodGet, "/admin/v1/placement-plan", "")
	if status != http.StatusOK {
		t.Fatalf("placement plan: %d %s", status, raw)
	}
	if !strings.Contains(string(raw), "e2e-chat") {
		t.Fatalf("placement did not propose the only enabled template for a qualifying card:\n%s", raw)
	}
	status, raw = p.controller(http.MethodPost, "/admin/v1/placement-plan/apply", "")
	if status != http.StatusOK {
		t.Fatalf("apply placement: %d %s", status, raw)
	}

	// The agent pulls its desired state. This is the document a real
	// host builds its compose file AND its attach document from, so
	// every field it needs for both has to be here.
	doc := h.desiredState(t, p)
	if len(doc.Services) != 1 {
		t.Fatalf("expected one service in the desired state, got %d: %+v", len(doc.Services), doc)
	}
	svc := doc.Services[0]
	if svc.Capability == "" || svc.Identity["openai.model"] == "" {
		t.Fatalf("the desired state does not say what to declare — an agent built from this "+
			"attaches a runner that matches no offer: %+v", svc)
	}
	if !strings.Contains(svc.ComposeFragment, "example.invalid/e2e-chat:1") {
		t.Fatalf("the compose fragment does not name the template's image:\n%s", svc.ComposeFragment)
	}
	if len(svc.DeviceIDs) != 1 || svc.DeviceIDs[0] != e2eGPUUUID {
		t.Fatalf("the service is not pinned to the card it was placed on: %v", svc.DeviceIDs)
	}

	// And the seam that has broken twice: the attach document an agent
	// builds FROM that desired state, accepted by the broker.
	fromDesired := attach(t, p.brokerURL, hostDocument(
		h.ID, h.AttachCredential, svc.Name, svc.Capability, svc.Identity["openai.model"], svc.DeviceIDs[0]), modeJob)
	fromDesired.requireAccepted()

	// Certification: the pool proves the runner works before selling
	// it. The run is started against the assignment and completed with
	// the verdict.
	assignmentID := svc.AssignmentID
	status, raw = p.controller(http.MethodPost,
		"/admin/v1/template-assignments/"+assignmentID+"/certification/start", "")
	if status != http.StatusOK {
		t.Fatalf("start certification: %d %s", status, raw)
	}
	var run struct {
		ID string `json:"id"`
	}
	decode(t, raw, &run)
	if run.ID == "" {
		t.Fatalf("certification start returned no run id: %s", raw)
	}
	status, raw = p.controller(http.MethodPost,
		"/admin/v1/certification-runs/"+run.ID+"/complete", `{"passed":true}`)
	if status != http.StatusOK {
		t.Fatalf("complete certification: %d %s", status, raw)
	}

	// The ladder turns a passed certification into a share of the
	// pool's work. Earning is the outcome the member actually cares
	// about, so it is what this asserts.
	status, raw = p.controller(http.MethodPost, "/admin/v1/ladder/run", "")
	if status != http.StatusOK {
		t.Fatalf("ladder run: %d %s", status, raw)
	}
	status, raw = p.controller(http.MethodGet, "/admin/v1/ladder/state", "")
	if status != http.StatusOK {
		t.Fatalf("ladder state: %d %s", status, raw)
	}
	var ladder struct {
		Placements []struct {
			AssignmentID string `json:"assignment_id"`
			State        string `json:"state"`
			Role         string `json:"role"`
		} `json:"placements"`
	}
	decode(t, raw, &ladder)
	var placed bool
	for _, entry := range ladder.Placements {
		if entry.AssignmentID != assignmentID {
			continue
		}
		placed = true
		// Probation, not full share: a card that has passed
		// certification but served no real traffic yet has proven it
		// works, not that it works under load. Anything else here is
		// the ladder either stalling a good member or handing an
		// unproven one the full share.
		if entry.State != "probationary_real_traffic" {
			t.Fatalf("a certified assignment sits at %q, so the member is not earning", entry.State)
		}
		if entry.Role != "primary" {
			t.Fatalf("role = %q, want primary", entry.Role)
		}
	}
	if !placed {
		t.Fatalf("the ladder does not know about the certified assignment:\n%s", raw)
	}

	// And the end state the member actually cares about: the broker is
	// selling their card's work. This is the whole chain resolved —
	// the template was priced, the offer pushed, the runner attached
	// and matched, the shape frozen, and the offering advertised.
	eventually(t, "the offer to be advertised with an eligible runner", 30*time.Second, func() error {
		status, raw := p.broker(http.MethodGet, "/admin/v1/offers/e2e-chat", "")
		if status != http.StatusOK {
			return fmt.Errorf("status %d: %s", status, raw)
		}
		var view struct {
			State      string `json:"state"`
			Advertised bool   `json:"advertised"`
			Runners    struct {
				Eligible int `json:"eligible"`
			} `json:"runners"`
		}
		decode(t, raw, &view)
		if !view.Advertised || view.Runners.Eligible == 0 {
			return fmt.Errorf("state=%s advertised=%v eligible=%d",
				view.State, view.Advertised, view.Runners.Eligible)
		}
		return nil
	})

	// The broker ran the template's certification recipe over the
	// attach tunnel to get there — the runner was asked to do real
	// work, not just to exist.
	if served := runner.served(); len(served) == 0 {
		t.Fatal("the offer froze without the broker ever asking the runner to serve anything")
	}

	// The public registry is where a gateway looks. An offering that
	// never reaches it is not for sale, however healthy it looks
	// on the admin API.
	status, raw = p.do(http.MethodGet, p.brokerURL+"/registry/offerings", "", "")
	if status != http.StatusOK {
		t.Fatalf("registry: %d %s", status, raw)
	}
	if !strings.Contains(string(raw), "e2e-chat") {
		t.Fatalf("the certified offering is absent from the public registry:\n%s", raw)
	}
}
