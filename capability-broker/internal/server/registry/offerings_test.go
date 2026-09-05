package registry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
)

func TestOfferTuple_EmitsEmptyConstraintsBlock(t *testing.T) {
	cfg := &config.Config{
		Identity: config.Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Offers: []config.Offer{{
			OfferingID: "zerank-2-default",
			Capability: "rerank",
			Protocol:   "paid-job/v1",
			Price:      config.Price{AmountWei: "1", PerUnits: 1},
		}},
	}

	payload := BuildOfferings(cfg)
	tuple := OfferTuple(cfg.Offers[0], FrozenShape{Projection: runnerattach.Projection{
		Transports: []string{"unary"},
		WorkUnit:   runnerattach.WorkUnit{Name: "requests"},
	}})
	if tuple == nil {
		t.Fatal("no tuple composed for a paid-job offer")
	}
	if tuple.Constraints == nil {
		t.Fatal("constraints is nil; want empty map")
	}
	payload.Capabilities = append(payload.Capabilities, *tuple)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"constraints":{}`) {
		t.Fatalf("marshalled payload missing empty constraints block: %s", raw)
	}
}

// TestOfferTuple_EmitsDeclaredAxes pins the contract the coordinator
// depends on: every paid-* offering advertises exactly one axes object,
// in the manifest's published vocabulary. Emitting protocol without axes
// produces manifests that fail schema validation downstream.
func TestOfferTuple_EmitsDeclaredAxes(t *testing.T) {
	job := OfferTuple(
		config.Offer{
			OfferingID: "default", Capability: "cap:job", Protocol: "paid-job/v1",
			Price: config.Price{AmountWei: "1", PerUnits: 1},
		},
		FrozenShape{Projection: runnerattach.Projection{
			Transports: []string{"unary", "stream"},
			WorkUnit:   runnerattach.WorkUnit{Name: "tokens"},
		}},
	)
	if job == nil {
		t.Fatal("no tuple composed for a paid-job offer")
	}
	if job.Session != nil {
		t.Fatal("paid-job offering carries session axes")
	}
	if job.Job == nil || len(job.Job.Transports) != 2 || job.Job.Transports[0] != "unary" {
		t.Fatalf("job axes wrong: %+v", job.Job)
	}

	sess := OfferTuple(
		config.Offer{
			OfferingID: "default", Capability: "cap:sess", Protocol: "paid-session/v1",
			Price: config.Price{AmountWei: "10", PerUnits: 1},
			SessionPolicy: &config.SessionPolicy{
				Heartbeat:            config.SessionHeartbeat{IntervalSeconds: 5, MissedThreshold: 4},
				LeaseMaxSeconds:      1800,
				RunwayIncrementUnits: 500,
			},
		},
		FrozenShape{Projection: runnerattach.Projection{
			DescriptorSchemas: []string{"sfu-room/v1"},
			Metering:          "runner-reported",
			WorkUnit:          runnerattach.WorkUnit{Name: "participant_minutes"},
		}},
	)
	if sess == nil {
		t.Fatal("no tuple composed for a paid-session offer")
	}
	if sess.Job != nil {
		t.Fatal("paid-session offering carries job axes")
	}
	if sess.Session == nil {
		t.Fatal("paid-session offering has no session axes")
	}
	// The runner-declared half of the axes is relayed from the frozen
	// shape, not re-derived: what is advertised is what was certified.
	if sess.Session.DescriptorSchema != "sfu-room/v1" {
		t.Fatalf("descriptor_schema %q", sess.Session.DescriptorSchema)
	}
	if sess.Session.Metering != "runner-reported" {
		t.Fatalf("metering %q", sess.Session.Metering)
	}
	// Required-by-schema fields must be present even when the operator
	// left them unset — that is what the tuple defaults are for.
	if sess.Session.Attachment != "external" || sess.Session.Refill != "extensible" {
		t.Fatalf("defaulted axes wrong: %+v", sess.Session)
	}
	if sess.Session.Heartbeat == nil || sess.Session.Heartbeat.MissedThreshold != 4 {
		t.Fatalf("heartbeat %+v", sess.Session.Heartbeat)
	}
	// The operator's flat lease cap becomes the manifest's lease object.
	if sess.Session.Lease == nil || sess.Session.Lease.Policy != "funding-tracking" ||
		sess.Session.Lease.MaxSeconds != 1800 {
		t.Fatalf("lease %+v", sess.Session.Lease)
	}
	if sess.Session.RunwayIncrementUnits != 500 {
		t.Fatalf("runway increment %d", sess.Session.RunwayIncrementUnits)
	}
}

// A runner's declared session_params shape reaches gateways verbatim so
// they can validate before opening, instead of learning the requirement
// from a create-time failure after payment was validated. The broker
// relays it and never enforces it.
func TestOfferTuple_RelaysSessionParamsSchema(t *testing.T) {
	schema := json.RawMessage(`{"required":["room_name"]}`)
	offer := config.Offer{
		OfferingID: "default", Capability: "cap:sess", Protocol: "paid-session/v1",
		Price: config.Price{AmountWei: "10", PerUnits: 1},
	}
	shape := FrozenShape{
		Projection: runnerattach.Projection{
			DescriptorSchemas: []string{"sfu-room/v1"},
			Metering:          "runner-reported",
			WorkUnit:          runnerattach.WorkUnit{Name: "participant_seconds"},
		},
		SessionParamsSchema: schema,
	}
	sess := OfferTuple(offer, shape).Session
	if sess == nil || len(sess.SessionParamsSchema) == 0 {
		t.Fatalf("session params schema not advertised: %+v", sess)
	}
	if string(sess.SessionParamsSchema) != string(schema) {
		t.Fatalf("schema altered in transit: %s", sess.SessionParamsSchema)
	}

	// A capability whose runner declared nothing advertises nothing —
	// the field must stay absent rather than becoming empty JSON.
	shape.SessionParamsSchema = nil
	if s := OfferTuple(offer, shape).Session.SessionParamsSchema; len(s) != 0 {
		t.Fatalf("absent schema became %q", s)
	}
}

// A transcription offering advertises its estimator, because a caller
// funding a multipart upload cannot derive a ceiling from the request
// parameters the way a JSON workload can.
func TestOfferingAdvertisesClientEstimator(t *testing.T) {
	est := estimatorFor(map[string]any{"type": "multipart-audio-duration"})
	if est == nil {
		t.Fatal("no estimator advertised for multipart-audio-duration")
	}
	if est.ID != "multipart-audio-duration/v1" {
		t.Fatalf("id = %q", est.ID)
	}
	if est.Rounding != "ceil-to-whole-seconds" {
		t.Fatalf("rounding = %q; a client must round the same way the seller bills", est.Rounding)
	}
	if est.Exactness != "exact-or-reject" {
		t.Fatalf("exactness = %q; a ceiling built on an estimate underfunds or overcharges",
			est.Exactness)
	}
	// The fixtures are the contract. Two independently-owned
	// implementations agree because they run the same vectors, so a
	// disagreement surfaces as a failing test rather than as a
	// settlement that exceeds the ceiling a caller funded.
	if est.Fixtures == "" {
		t.Fatal("a client told to reproduce an estimator needs the fixtures that pin it")
	}
	// And no package. The field names a canonical client library a
	// caller can install; there is no longer one, and the name this used
	// to carry was never published — so a caller that trusted the field
	// got a 404 for its trouble. Absent is the honest answer, and this
	// asserts it rather than letting a well-meaning re-add slip back in.
	if est.Package != "" {
		t.Fatalf("package = %q; implementations are independently owned, so there is no "+
			"canonical client library to name", est.Package)
	}
}

// Everything else stays unadvertised. How a seller counts is its own
// business and no counterparty gates on it; the exception exists only
// where a client genuinely has to reproduce the number.
func TestOtherExtractorsAdvertiseNoEstimator(t *testing.T) {
	for _, typ := range []string{"openai-usage", "request-formula", "bytes-counted",
		"seconds-elapsed", "response-jsonpath", ""} {
		if est := estimatorFor(map[string]any{"type": typ}); est != nil {
			t.Fatalf("extractor %q advertised estimator %q; extractors are seller-side "+
				"implementation detail unless a client must reproduce them", typ, est.ID)
		}
	}
}
