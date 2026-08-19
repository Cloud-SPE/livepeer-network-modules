package policy

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/diff"
)

type tup struct {
	capID, offID, protocol, workUnit, price, workerURL string
	job, session, extra, constraints                   map[string]any
}

func manifestJSON(t *testing.T, spec string, seq uint64, eth string, tuples ...tup) []byte {
	t.Helper()
	caps := make([]any, 0, len(tuples))
	for _, tp := range tuples {
		entry := map[string]any{
			"capability_id":      tp.capID,
			"offering_id":        tp.offID,
			"protocol":           tp.protocol,
			"work_unit":          map[string]any{"name": tp.workUnit},
			"price_per_unit_wei": tp.price,
			"worker_url":         tp.workerURL,
		}
		if tp.job != nil {
			entry["job"] = tp.job
		}
		if tp.session != nil {
			entry["session"] = tp.session
		}
		if tp.extra != nil {
			entry["extra"] = tp.extra
		}
		if tp.constraints != nil {
			entry["constraints"] = tp.constraints
		}
		caps = append(caps, entry)
	}
	body, err := json.Marshal(map[string]any{
		"spec_version":    spec,
		"publication_seq": seq,
		"issued_at":       "2026-06-10T00:00:00Z",
		"expires_at":      "2026-06-11T00:00:00Z",
		"orch":            map[string]any{"eth_address": eth},
		"capabilities":    caps,
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func baseTuple() tup {
	return tup{
		capID: "openai:chat-completions", offID: "vllm-h100", protocol: "paid-job/v1",
		job:      map[string]any{"transports": []any{"unary", "stream"}},
		workUnit: "tokens", price: "1000", workerURL: "https://a.workers.example-orch.net/",
	}
}

// sessionTuple is the paid-session counterpart: the axes object carries
// the semantics the old mode string used to smuggle in its name.
func sessionTuple() tup {
	return tup{
		capID: "video:transcode.live", offID: "h264-1080p30", protocol: "paid-session/v1",
		session: map[string]any{
			"descriptor_schema":      "rtmp-hls/v1",
			"metering":               "runner-reported",
			"refill":                 "extensible",
			"heartbeat":              map[string]any{"interval_seconds": 10, "missed_threshold": 3},
			"runway_increment_units": 60000,
		},
		workUnit: "video-frame-megapixel", price: "1000", workerURL: "https://a.workers.example-orch.net/",
	}
}

func computeDiff(t *testing.T, before, after []byte) *diff.Result {
	t.Helper()
	d, err := diff.Compute(before, after)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func defaultBounds() BenignBounds {
	return BenignBounds{
		PriceDeltaMaxPct:         10,
		AllowTupleRemoval:        true,
		WorkerURLDomainAllowlist: []string{"workers.example-orch.net"},
	}
}

const (
	ethA = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	ethB = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestClassify_NoOpAndRenewalSplit(t *testing.T) {
	before := manifestJSON(t, "0.1.0", 4, ethA, baseTuple())
	after := manifestJSON(t, "0.1.0", 5, ethA, baseTuple())
	d := computeDiff(t, before, after)

	in := ClassifyInput{Bounds: defaultBounds(), RenewalThreshold: 8 * time.Hour}

	in.RemainingValidity = 20 * time.Hour
	if got := Classify(d, in); got.Class != ClassNoOp {
		t.Fatalf("above threshold: class=%s want no_op (findings: %+v)", got.Class, got.Findings)
	}

	in.RemainingValidity = 5 * time.Hour
	if got := Classify(d, in); got.Class != ClassRenewal {
		t.Fatalf("below threshold: class=%s want renewal", got.Class)
	}
}

func TestClassify_FirstSignIsCritical(t *testing.T) {
	after := manifestJSON(t, "0.1.0", 0, ethA, baseTuple())
	d := computeDiff(t, nil, after)
	got := Classify(d, ClassifyInput{Bounds: defaultBounds(), FirstSign: true})
	if got.Class != ClassCritical {
		t.Fatalf("class=%s want critical", got.Class)
	}
	if got.Findings[0].Code != CodeFirstSign {
		t.Fatalf("code=%s want %s", got.Findings[0].Code, CodeFirstSign)
	}
}

func TestClassify_ChangeTable(t *testing.T) {
	mutate := func(f func(*tup)) tup {
		tp := baseTuple()
		f(&tp)
		return tp
	}
	cases := []struct {
		name      string
		bounds    BenignBounds
		after     []tup
		wantClass Class
		wantCode  string
	}{
		{
			name:      "tuple added",
			after:     []tup{baseTuple(), {capID: "new:cap", offID: "o", protocol: "paid-job/v1", job: map[string]any{"transports": []any{"unary"}}, workUnit: "x", price: "1", workerURL: "https://a.workers.example-orch.net/"}},
			wantClass: ClassCritical, wantCode: CodeTupleAdded,
		},
		{
			name:      "tuple removed allowed",
			after:     nil,
			wantClass: ClassBenign, wantCode: CodeTupleRemoved,
		},
		{
			name:      "tuple removed disallowed",
			bounds:    BenignBounds{PriceDeltaMaxPct: 10, AllowTupleRemoval: false},
			after:     nil,
			wantClass: ClassCritical, wantCode: CodeTupleRemoved,
		},
		{
			name:      "price increase within bound",
			after:     []tup{mutate(func(tp *tup) { tp.price = "1100" })},
			wantClass: ClassBenign, wantCode: CodePriceWithinBound,
		},
		{
			name:      "price decrease within bound",
			after:     []tup{mutate(func(tp *tup) { tp.price = "950" })},
			wantClass: ClassBenign, wantCode: CodePriceWithinBound,
		},
		{
			name:      "price increase beyond bound",
			after:     []tup{mutate(func(tp *tup) { tp.price = "1500" })},
			wantClass: ClassCritical, wantCode: CodePriceBeyondBound,
		},
		{
			// Q1 proposal: decreases are bounded too — a fat-finger
			// 99% cut must hold for review.
			name:      "price decrease beyond bound",
			after:     []tup{mutate(func(tp *tup) { tp.price = "10" })},
			wantClass: ClassCritical, wantCode: CodePriceBeyondBound,
		},
		{
			name:      "price unparseable",
			after:     []tup{mutate(func(tp *tup) { tp.price = "1.5e3" })},
			wantClass: ClassCritical, wantCode: CodePriceUnparseable,
		},
		{
			name:      "worker_url within allowlist suffix",
			after:     []tup{mutate(func(tp *tup) { tp.workerURL = "https://b.workers.example-orch.net/" })},
			wantClass: ClassBenign, wantCode: CodeWorkerURLAllowed,
		},
		{
			name:      "worker_url equals allowlist entry",
			after:     []tup{mutate(func(tp *tup) { tp.workerURL = "https://workers.example-orch.net/" })},
			wantClass: ClassBenign, wantCode: CodeWorkerURLAllowed,
		},
		{
			name:      "worker_url outside allowlist",
			after:     []tup{mutate(func(tp *tup) { tp.workerURL = "https://evil.example.com/" })},
			wantClass: ClassCritical, wantCode: CodeWorkerURLDisallowed,
		},
		{
			// No dot-boundary cheating: evilworkers.example-orch.net
			// must not match workers.example-orch.net.
			name:      "worker_url suffix without dot boundary",
			after:     []tup{mutate(func(tp *tup) { tp.workerURL = "https://evilworkers.example-orch.net/" })},
			wantClass: ClassCritical, wantCode: CodeWorkerURLDisallowed,
		},
		{
			name:      "extra changed",
			after:     []tup{mutate(func(tp *tup) { tp.extra = map[string]any{"region": "us-east-1"} })},
			wantClass: ClassCritical, wantCode: CodeExtraChanged,
		},
		{
			name:      "constraints changed",
			after:     []tup{mutate(func(tp *tup) { tp.constraints = map[string]any{"max_tokens": 4096} })},
			wantClass: ClassCritical, wantCode: CodeConstraintsChanged,
		},
		{
			name:      "protocol changed",
			after:     []tup{mutate(func(tp *tup) { tp.protocol = "paid-job/v2" })},
			wantClass: ClassCritical, wantCode: CodeProtocolChanged,
		},
		{
			// A transport the broker did not previously serve is new
			// surface area, not a cosmetic edit.
			name: "job transport added",
			after: []tup{mutate(func(tp *tup) {
				tp.job = map[string]any{"transports": []any{"unary", "stream", "multipart"}}
			})},
			wantClass: ClassCritical, wantCode: CodeJobAxesChanged,
		},
		{
			name: "job transport removed",
			after: []tup{mutate(func(tp *tup) {
				tp.job = map[string]any{"transports": []any{"unary"}}
			})},
			wantClass: ClassCritical, wantCode: CodeJobAxesChanged,
		},
		{
			// The old vocabulary spelled this as one mode-string swap.
			// Split across protocol + axes, it must still hold.
			name: "protocol flipped from job to session",
			after: []tup{mutate(func(tp *tup) {
				tp.protocol = "paid-session/v1"
				tp.job = nil
				tp.session = map[string]any{"descriptor_schema": "rtmp-hls/v1", "metering": "runner-reported"}
			})},
			wantClass: ClassCritical, wantCode: CodeProtocolChanged,
		},
		{
			name:      "work_unit changed",
			after:     []tup{mutate(func(tp *tup) { tp.workUnit = "frames" })},
			wantClass: ClassCritical, wantCode: CodeWorkUnitChanged,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bounds := tc.bounds
			if bounds.PriceDeltaMaxPct == 0 && bounds.WorkerURLDomainAllowlist == nil && !bounds.AllowTupleRemoval {
				bounds = defaultBounds()
			}
			before := manifestJSON(t, "0.1.0", 4, ethA, baseTuple())
			after := manifestJSON(t, "0.1.0", 5, ethA, tc.after...)
			got := Classify(computeDiff(t, before, after), ClassifyInput{Bounds: bounds, RenewalThreshold: 8 * time.Hour, RemainingValidity: 20 * time.Hour})
			if got.Class != tc.wantClass {
				t.Fatalf("class=%s want %s (findings: %+v)", got.Class, tc.wantClass, got.Findings)
			}
			found := false
			for _, f := range got.Findings {
				if f.Code == tc.wantCode {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing finding code %s in %+v", tc.wantCode, got.Findings)
			}
		})
	}
}

// Every session axis is critical — the gating ones because a
// counterparty routes, meters, or refills on them, the advisory ones
// because BenignBounds has no bound to grade them against. The detail
// string must name the axis that moved so the held-queue entry is
// reviewable without re-diffing.
func TestClassify_SessionAxesChangesAreCritical(t *testing.T) {
	mutate := func(f func(map[string]any)) tup {
		tp := sessionTuple()
		f(tp.session)
		return tp
	}
	cases := []struct {
		name       string
		after      tup
		wantDetail string
	}{
		{
			// Gateways MUST NOT open sessions whose schema they do not
			// implement; swapping it re-points every buyer.
			name:       "descriptor_schema",
			after:      mutate(func(s map[string]any) { s["descriptor_schema"] = "webrtc/v1" }),
			wantDetail: "session axes changed: descriptor_schema",
		},
		{
			// The clearinghouse gates top-up on this field.
			name:       "refill bounded",
			after:      mutate(func(s map[string]any) { s["refill"] = "bounded" }),
			wantDetail: "session axes changed: refill",
		},
		{
			// Who counts the money.
			name:       "metering origin",
			after:      mutate(func(s map[string]any) { s["metering"] = "broker-observed" }),
			wantDetail: "session axes changed: metering",
		},
		{
			// Whether the data plane transits the broker at all.
			name:       "attachment declared",
			after:      mutate(func(s map[string]any) { s["attachment"] = "inband-ws" }),
			wantDetail: "session axes changed: attachment",
		},
		{
			// Advisory, but still not gradable against any bound.
			name: "heartbeat interval",
			after: mutate(func(s map[string]any) {
				s["heartbeat"] = map[string]any{"interval_seconds": 600, "missed_threshold": 3}
			}),
			wantDetail: "session axes changed: heartbeat",
		},
		{
			name:       "runway increment advisory",
			after:      mutate(func(s map[string]any) { s["runway_increment_units"] = 1 }),
			wantDetail: "session axes changed: runway_increment_units",
		},
	}
	before := manifestJSON(t, "0.1.0", 4, ethA, sessionTuple())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			after := manifestJSON(t, "0.1.0", 5, ethA, tc.after)
			got := Classify(computeDiff(t, before, after), ClassifyInput{
				Bounds: defaultBounds(), RenewalThreshold: 8 * time.Hour, RemainingValidity: 20 * time.Hour,
			})
			if got.Class != ClassCritical {
				t.Fatalf("class=%s want critical (findings: %+v)", got.Class, got.Findings)
			}
			if len(got.Findings) != 1 || got.Findings[0].Code != CodeSessionAxesChanged {
				t.Fatalf("findings=%+v want a single %s", got.Findings, CodeSessionAxesChanged)
			}
			if got.Findings[0].Detail != tc.wantDetail {
				t.Fatalf("detail=%q want %q", got.Findings[0].Detail, tc.wantDetail)
			}
		})
	}
}

// A session tuple whose axes are untouched is still a no-op: grading
// axes critical must not make every republish hold.
func TestClassify_SessionAxesUnchangedIsNoOp(t *testing.T) {
	before := manifestJSON(t, "0.1.0", 4, ethA, sessionTuple())
	after := manifestJSON(t, "0.1.0", 5, ethA, sessionTuple())
	got := Classify(computeDiff(t, before, after), ClassifyInput{
		Bounds: defaultBounds(), RenewalThreshold: 8 * time.Hour, RemainingValidity: 20 * time.Hour,
	})
	if got.Class != ClassNoOp {
		t.Fatalf("class=%s want no_op (findings: %+v)", got.Class, got.Findings)
	}
}

func TestClassify_HighestClassWins(t *testing.T) {
	// A within-bound price change (benign) plus a new tuple
	// (critical): the candidate is critical.
	changed := baseTuple()
	changed.price = "1050"
	before := manifestJSON(t, "0.1.0", 4, ethA, baseTuple())
	after := manifestJSON(t, "0.1.0", 5, ethA, changed,
		tup{capID: "new:cap", offID: "o", protocol: "paid-job/v1", job: map[string]any{"transports": []any{"unary"}}, workUnit: "x", price: "1", workerURL: "https://x.example/"})
	got := Classify(computeDiff(t, before, after), ClassifyInput{Bounds: defaultBounds()})
	if got.Class != ClassCritical {
		t.Fatalf("class=%s want critical", got.Class)
	}
	if len(got.Findings) != 2 {
		t.Fatalf("findings=%d want 2 (benign survives in the report)", len(got.Findings))
	}

	// The same benign change plus an eth_address flip: forbidden.
	afterForbidden := manifestJSON(t, "0.1.0", 5, ethB, changed)
	got = Classify(computeDiff(t, before, afterForbidden), ClassifyInput{Bounds: defaultBounds()})
	if got.Class != ClassForbidden {
		t.Fatalf("class=%s want forbidden", got.Class)
	}
}

func TestClassify_ForbiddenHeaderChanges(t *testing.T) {
	before := manifestJSON(t, "0.1.0", 4, ethA, baseTuple())
	for _, tc := range []struct {
		name  string
		after []byte
		code  string
	}{
		{"eth_address change", manifestJSON(t, "0.1.0", 5, ethB, baseTuple()), CodeEthAddressChanged},
		{"spec_version change", manifestJSON(t, "0.2.0", 5, ethA, baseTuple()), CodeSpecVersionChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(computeDiff(t, before, tc.after), ClassifyInput{Bounds: defaultBounds()})
			if got.Class != ClassForbidden {
				t.Fatalf("class=%s want forbidden", got.Class)
			}
			if got.Findings[0].Code != tc.code {
				t.Fatalf("code=%s want %s", got.Findings[0].Code, tc.code)
			}
		})
	}
}

func TestDecide_Table(t *testing.T) {
	phase1 := Policy{AutoSign: AutoSign{Renewal: true, Benign: false}}
	phase2 := Policy{AutoSign: AutoSign{Renewal: true, Benign: true}}
	allOff := Policy{}
	cases := []struct {
		class      Class
		policy     Policy
		wantAction Action
		wantShadow bool
	}{
		{ClassNoOp, phase1, ActionSkip, false},
		{ClassRenewal, phase1, ActionAutoSign, false},
		{ClassRenewal, allOff, ActionHold, false},
		{ClassBenign, phase1, ActionHold, true},
		{ClassBenign, phase2, ActionAutoSign, false},
		{ClassCritical, phase2, ActionHold, false},
		{ClassForbidden, phase2, ActionRefuse, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/renewal=%v,benign=%v", tc.class, tc.policy.AutoSign.Renewal, tc.policy.AutoSign.Benign), func(t *testing.T) {
			d := Decide(Classification{Class: tc.class}, tc.policy)
			if d.Action != tc.wantAction {
				t.Fatalf("action=%s want %s", d.Action, tc.wantAction)
			}
			if d.ShadowAutoSign != tc.wantShadow {
				t.Fatalf("shadow=%v want %v", d.ShadowAutoSign, tc.wantShadow)
			}
		})
	}
}
