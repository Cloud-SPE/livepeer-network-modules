package revenue

import (
	"strings"
	"testing"
	"time"
)

func TestReportRejectsUnknownStaleAndWrongSource(t *testing.T) {
	now := time.Now()
	source := Source{PoolID: "pool_us", SourceID: "0x" + strings.Repeat("a", 64), BrokerID: "audio-broker", ChainID: 42161, Payee: "0x" + strings.Repeat("b", 40)}
	report := Report{PoolID: source.PoolID, SourceID: source.SourceID, BrokerID: source.BrokerID, ChainID: source.ChainID, Payee: source.Payee, Round: 100, RevenueWei: "0", Complete: true, CompleteThroughRound: 100, ObservedAt: now, ObservedHead: 1000, FinalizedBlock: 996, ObservedHeadHash: "0x" + strings.Repeat("c", 64), FinalizedBlockHash: "0x" + strings.Repeat("d", 64), InclusionDigest: strings.Repeat("e", 64)}
	if err := report.Validate(source, 100, now); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Report){
		func(r *Report) { r.SourceID = "0x" + strings.Repeat("f", 64) },
		func(r *Report) { r.PoolID = "pool_eu" },
		func(r *Report) { r.BrokerID = "llm-broker" },
		func(r *Report) { r.Complete = false },
		func(r *Report) { r.CompleteThroughRound = 99 },
		func(r *Report) { r.ObservedAt = now.Add(-3 * time.Minute) },
		func(r *Report) { r.RevenueWei = "-1" },
		func(r *Report) { r.FinalizedBlockHash = "0x" + strings.Repeat("z", 64) },
	} {
		candidate := report
		mutate(&candidate)
		if err := candidate.Validate(source, 100, now); err == nil {
			t.Fatalf("accepted %+v", candidate)
		}
	}
}
