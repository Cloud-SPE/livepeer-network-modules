package scenarios

import (
	"errors"
	"fmt"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/harness"
)

// ---------------------------------------------------------------------------
// runner-attach §9
//
// These scenarios play the runner side of the attach contract against the
// broker-under-test. They need two things the paid-path scenarios do not:
// the broker's WS attach endpoint, and an enrolled credential to present
// (`--attach-credential`, or auto mode wiring once the reference broker
// implements plan 0043 items 6–7). Until both exist every scenario skips
// with the reason, so the suite stays green on a pre-0043 broker while the
// fixtures are already pinned to their clauses.

func attachScenarios() []harness.Scenario {
	return []harness.Scenario{
		{Name: "attach/accepts-minimal-job", Spec: "runner-attach §3.2/§9", Run: func(c *harness.Ctx) error {
			conn, err := attachOrSkip(c)
			if err != nil {
				return err
			}
			defer conn.Close()
			res, err := conn.Register(minimalJobDoc(c, "a1"))
			if err != nil {
				return err
			}
			if res.Document != "accepted" {
				return fmt.Errorf("document %q, reasons %+v; want accepted", res.Document, res.Reasons)
			}
			if len(res.Capabilities) != 1 || res.Capabilities[0].Status != "accepted" {
				return fmt.Errorf("capabilities %+v; want one accepted", res.Capabilities)
			}
			return nil
		}},
		{Name: "attach/accepts-minimal-session", Spec: "runner-attach §3.2/§9", Run: func(c *harness.Ctx) error {
			conn, err := attachOrSkip(c)
			if err != nil {
				return err
			}
			defer conn.Close()
			res, err := conn.Register(minimalSessionDoc(c, "s1"))
			if err != nil {
				return err
			}
			if res.Document != "accepted" || len(res.Capabilities) != 1 || res.Capabilities[0].Status != "accepted" {
				return fmt.Errorf("got %+v; want accepted document with one accepted capability", res)
			}
			return nil
		}},
		{Name: "attach/accepts-hardware-only", Spec: "runner-attach §3.1/§9", Run: func(c *harness.Ctx) error {
			conn, err := attachOrSkip(c)
			if err != nil {
				return err
			}
			defer conn.Close()
			doc := hostDoc(c, "hw1")
			doc["hardware"] = []any{gpu("GPU-conf-0001")}
			doc["capabilities"] = []any{}
			res, err := conn.Register(doc)
			if err != nil {
				return err
			}
			if res.Document != "accepted" || len(res.Capabilities) != 0 {
				return fmt.Errorf("got %+v; want accepted with zero capabilities", res)
			}
			return nil
		}},
		{Name: "attach/rejects-unknown-field", Spec: "runner-attach §4.1/§9", Run: func(c *harness.Ctx) error {
			conn, err := attachOrSkip(c)
			if err != nil {
				return err
			}
			defer conn.Close()
			doc := minimalJobDoc(c, "u1")
			doc["price"] = map[string]any{"amount_wei": "1"}
			res, err := conn.Register(doc)
			if err != nil {
				return err
			}
			return expectDocRejected(res, "unknown_field", "/price")
		}},
		{Name: "attach/rejects-unknown-capability-field", Spec: "runner-attach §4.1/§9", Run: func(c *harness.Ctx) error {
			conn, err := attachOrSkip(c)
			if err != nil {
				return err
			}
			defer conn.Close()
			doc := minimalJobDoc(c, "u2")
			doc["capabilities"].([]any)[0].(map[string]any)["capacity"] = map[string]any{"max_in_flight": 4}
			res, err := conn.Register(doc)
			if err != nil {
				return err
			}
			return expectDocRejected(res, "unknown_field", "/capabilities/0/capacity")
		}},
		{Name: "attach/rejects-bad-major", Spec: "runner-attach §4.1/§8/§9", Run: func(c *harness.Ctx) error {
			conn, err := attachOrSkip(c)
			if err != nil {
				return err
			}
			defer conn.Close()
			doc := minimalJobDoc(c, "v1")
			doc["contract_version"] = "2.0"
			res, err := conn.Register(doc)
			if err != nil {
				return err
			}
			return expectDocRejected(res, "contract_version_unsupported", "")
		}},
		{Name: "attach/rejects-credential-indistinguishably", Spec: "runner-attach §3.1.1/§4.1/§9", Run: func(c *harness.Ctx) error {
			if _, err := attachOrSkip(c); err != nil {
				return err
			}
			var bodies []string
			for _, tok := range []string{"lpc_unknown_" + c.RunID, "lpc_unknown2_" + c.RunID} {
				conn, err := c.DialAttach()
				if err != nil {
					return err
				}
				doc := minimalJobDoc(c, "c1")
				doc["credential"] = map[string]any{"kind": "bearer", "token": tok}
				res, err := conn.Register(doc)
				conn.Close()
				if err != nil {
					return err
				}
				if res.Document != "rejected" || harness.Reason(res.Reasons, "credential_rejected") == nil {
					return fmt.Errorf("token %q: got %+v; want credential_rejected", tok, res)
				}
				bodies = append(bodies, fmt.Sprintf("%+v", res.Reasons))
			}
			// Unknown vs unknown is the weakest form of the rule; expired
			// and revoked need admin-API setup, which the broker-admin
			// fixtures cover. The reasons list must at least be identical
			// across distinct unknown tokens (no token echo).
			if bodies[0] != bodies[1] {
				return fmt.Errorf("rejection differs between unknown tokens: %s vs %s", bodies[0], bodies[1])
			}
			return nil
		}},
		{Name: "attach/rejects-one-capability-keeps-others", Spec: "runner-attach §4.2/§9", Run: func(c *harness.Ctx) error {
			conn, err := attachOrSkip(c)
			if err != nil {
				return err
			}
			defer conn.Close()
			doc := minimalJobDoc(c, "k1")
			bad := jobCap(c, "bad")
			bad["work_unit"] = map[string]any{"name": "tokens", "extractor": map[string]any{"type": "no-such-extractor"}}
			doc["capabilities"] = append(doc["capabilities"].([]any), bad)
			res, err := conn.Register(doc)
			if err != nil {
				return err
			}
			if res.Document != "accepted" || len(res.Capabilities) != 2 {
				return fmt.Errorf("got %+v; want accepted document with two entries", res)
			}
			if res.Capabilities[0].Status != "accepted" {
				return fmt.Errorf("entry 0 %+v; want accepted", res.Capabilities[0])
			}
			r := harness.Reason(res.Capabilities[1].Reasons, "extractor_unknown")
			if res.Capabilities[1].Status != "rejected" || r == nil {
				return fmt.Errorf("entry 1 %+v; want rejected with extractor_unknown", res.Capabilities[1])
			}
			if r.Declared != "no-such-extractor" || r.Expected == "" {
				return fmt.Errorf("reason %+v; want declared and expected both named", *r)
			}
			return nil
		}},
		{Name: "attach/rejects-extractor-on-session", Spec: "runner-attach §3.2/§4.2/§9", Run: func(c *harness.Ctx) error {
			conn, err := attachOrSkip(c)
			if err != nil {
				return err
			}
			defer conn.Close()
			doc := minimalSessionDoc(c, "x1")
			doc["capabilities"].([]any)[0].(map[string]any)["work_unit"] = map[string]any{
				"name": c.SessionUnit, "extractor": map[string]any{"type": "seconds-elapsed"}}
			res, err := conn.Register(doc)
			if err != nil {
				return err
			}
			if res.Document != "accepted" || len(res.Capabilities) != 1 || res.Capabilities[0].Status != "rejected" ||
				harness.Reason(res.Capabilities[0].Reasons, "schema_violation") == nil {
				return fmt.Errorf("got %+v; want the entry rejected with schema_violation", res)
			}
			return nil
		}},
		{Name: "attach/rejects-requirements-unmet", Spec: "runner-attach §4.2/§9", Run: func(c *harness.Ctx) error {
			conn, err := attachOrSkip(c)
			if err != nil {
				return err
			}
			defer conn.Close()
			doc := minimalJobDoc(c, "r1")
			doc["hardware"] = []any{gpu("GPU-conf-small")}
			doc["capabilities"].([]any)[0].(map[string]any)["requirements"] = map[string]any{"gpu_vram_min_bytes": 1 << 50}
			res, err := conn.Register(doc)
			if err != nil {
				return err
			}
			if res.Document != "accepted" || len(res.Capabilities) != 1 ||
				harness.Reason(res.Capabilities[0].Reasons, "requirements_unmet") == nil {
				return fmt.Errorf("got %+v; want entry rejected with requirements_unmet", res)
			}
			return nil
		}},
		{Name: "attach/rejects-duplicate-identity", Spec: "runner-attach §4.1/§9", Run: func(c *harness.Ctx) error {
			conn, err := attachOrSkip(c)
			if err != nil {
				return err
			}
			defer conn.Close()
			doc := minimalJobDoc(c, "d1")
			dup := jobCap(c, "dup")
			doc["capabilities"] = append(doc["capabilities"].([]any), dup)
			res, err := conn.Register(doc)
			if err != nil {
				return err
			}
			return expectDocRejected(res, "duplicate_capability", "")
		}},
		{Name: "attach/first-before-anything", Spec: "runner-attach §2/§9", Run: func(c *harness.Ctx) error {
			conn, err := attachOrSkip(c)
			if err != nil {
				return err
			}
			defer conn.Close()
			if err := conn.SendRaw(map[string]any{"type": "request", "id": "early", "method": "GET", "url": "/"}); err != nil {
				return err
			}
			return conn.ExpectClosed(10 * time.Second)
		}},
		// attach/replaces-on-resend, attach/never-mutates-offer and
		// attach/routes-by-local-id need the admin API and a matched
		// offer; they live with the broker-admin fixtures.
	}
}

func attachOrSkip(c *harness.Ctx) (*harness.AttachConn, error) {
	if c.AttachCredential == "" {
		return nil, fmt.Errorf("%w: no --attach-credential (broker predates runner-attach, plan 0043 item 7)", harness.ErrSkip)
	}
	conn, err := c.DialAttach()
	if errors.Is(err, harness.ErrAttachUnsupported) {
		return nil, fmt.Errorf("%w: %v", harness.ErrSkip, err)
	}
	return conn, err
}

func expectDocRejected(res *harness.AttachResult, code, field string) error {
	if res.Document != "rejected" {
		return fmt.Errorf("document %q; want rejected", res.Document)
	}
	if len(res.Capabilities) != 0 {
		return fmt.Errorf("capabilities present on a rejected document: %+v", res.Capabilities)
	}
	r := harness.Reason(res.Reasons, code)
	if r == nil {
		return fmt.Errorf("reasons %+v; want %s", res.Reasons, code)
	}
	if field != "" && r.Field != field {
		return fmt.Errorf("reason field %q; want %q", r.Field, field)
	}
	return nil
}

func hostDoc(c *harness.Ctx, tag string) harness.AttachDoc {
	return harness.AttachDoc{
		"contract_version": "1.0",
		"credential":       map[string]any{"kind": "bearer", "token": c.AttachCredential},
		"host_id":          c.AttachHostID,
		"agent_version":    "livepeer-conformance/" + c.RunID + "-" + tag,
		"hardware":         []any{},
		"capabilities":     []any{},
	}
}

func gpu(uuid string) map[string]any {
	return map[string]any{"gpu_uuid": uuid, "gpu_model": "Conformance GPU", "vram_bytes": 8 << 30}
}

func jobCap(c *harness.Ctx, localID string) map[string]any {
	return map[string]any{
		"capability_id":   c.JobCapability,
		"protocol":        "paid-job/v1",
		"local_id":        localID,
		"transports":      []any{"unary", "stream", "multipart"},
		"work_unit":       map[string]any{"name": c.JobUnit, "extractor": map[string]any{"type": "openai-usage"}},
		"paths":           map[string]any{"invoke": "/"},
		"readiness":       map[string]any{"type": "http-status", "path": "/"},
		"identity":        map[string]any{"provider": "conformance"},
		"schema_versions": map[string]any{"paid-job/v1": "1.0"},
	}
}

func minimalJobDoc(c *harness.Ctx, localID string) harness.AttachDoc {
	doc := hostDoc(c, localID)
	doc["capabilities"] = []any{jobCap(c, localID)}
	return doc
}

func minimalSessionDoc(c *harness.Ctx, localID string) harness.AttachDoc {
	doc := hostDoc(c, localID)
	doc["capabilities"] = []any{map[string]any{
		"capability_id":      c.SessionCapability,
		"protocol":           "paid-session/v1",
		"local_id":           localID,
		"descriptor_schemas": []any{"sfu-room/v1"},
		"metering":           "runner-reported",
		"work_unit":          map[string]any{"name": c.SessionUnit},
		"paths":              map[string]any{"create": "/sessions", "status": "/sessions/{id}", "terminate": "/sessions/{id}"},
		"readiness":          map[string]any{"type": "http-status", "path": "/"},
		"identity":           map[string]any{"provider": "conformance"},
		"schema_versions":    map[string]any{"paid-session/v1": "1.0", "sfu-room/v1": "1.0"},
	}}
	return doc
}
