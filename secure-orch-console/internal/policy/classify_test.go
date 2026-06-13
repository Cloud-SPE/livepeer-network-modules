package policy

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/diff"
)

type tup struct {
	capID, offID, mode, workUnit, price, workerURL string
	extra, constraints                             map[string]any
}

func manifestJSON(t *testing.T, spec string, seq uint64, eth string, tuples ...tup) []byte {
	t.Helper()
	caps := make([]any, 0, len(tuples))
	for _, tp := range tuples {
		entry := map[string]any{
			"capability_id":      tp.capID,
			"offering_id":        tp.offID,
			"interaction_mode":   tp.mode,
			"work_unit":          map[string]any{"name": tp.workUnit},
			"price_per_unit_wei": tp.price,
			"worker_url":         tp.workerURL,
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
		capID: "openai:chat-completions", offID: "vllm-h100", mode: "http-stream@v1",
		workUnit: "tokens", price: "1000", workerURL: "https://a.workers.example-orch.net/",
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
			after:     []tup{baseTuple(), {capID: "new:cap", offID: "o", mode: "m@v1", workUnit: "x", price: "1", workerURL: "https://a.workers.example-orch.net/"}},
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
			name:      "interaction_mode changed",
			after:     []tup{mutate(func(tp *tup) { tp.mode = "ws-realtime@v0" })},
			wantClass: ClassCritical, wantCode: CodeModeChanged,
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

func TestClassify_HighestClassWins(t *testing.T) {
	// A within-bound price change (benign) plus a new tuple
	// (critical): the candidate is critical.
	changed := baseTuple()
	changed.price = "1050"
	before := manifestJSON(t, "0.1.0", 4, ethA, baseTuple())
	after := manifestJSON(t, "0.1.0", 5, ethA, changed,
		tup{capID: "new:cap", offID: "o", mode: "m@v1", workUnit: "x", price: "1", workerURL: "https://x.example/"})
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
