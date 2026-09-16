package termspublication

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/regionalterms"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type broker struct {
	policy                      regionalterms.Policy
	unavailable, loseActivation bool
	pauses, activations         int
}

func (b *broker) TermsPolicy(context.Context) (regionalterms.Policy, error) {
	if b.unavailable {
		return regionalterms.Policy{}, fmt.Errorf("broker offline")
	}
	return b.policy, nil
}
func (b *broker) UpdateTermsPolicy(_ context.Context, r brokeradmin.TermsPolicyRequest) (regionalterms.Policy, error) {
	if b.unavailable {
		return regionalterms.Policy{}, fmt.Errorf("broker offline")
	}
	if b.policy.Revision == r.ExpectedRevision+1 && (r.Action == "pause" && b.policy.Paused || r.Action == "activate" && !b.policy.Paused && b.policy.Version == r.Version) {
		return b.policy, nil
	}
	if b.policy.Revision != r.ExpectedRevision {
		return b.policy, fmt.Errorf("revision changed")
	}
	b.policy.Revision++
	if r.Action == "pause" {
		b.policy.Paused = true
		b.pauses++
	} else {
		b.policy.Paused = false
		b.policy.Version = r.Version
		b.policy.EffectiveRound = r.EffectiveRound
		b.activations++
		if b.loseActivation {
			b.loseActivation = false
			return regionalterms.Policy{}, fmt.Errorf("activation accepted, reply lost")
		}
	}
	return b.policy, nil
}
func TestPublicationAllBrokerBarrierAndLostActivationReplySurviveRestart(t *testing.T) {
	dir := t.TempDir()
	st, err := repo.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	remotes := map[string]*broker{}
	var names []string
	for i, name := range []string{"transcode-broker", "audio-broker", "llm-broker"} {
		source := revenue.Source{PoolID: st.PoolID(), SourceID: "0x" + strings.Repeat(fmt.Sprintf("%x", i+1), 64), BrokerID: name, ChainID: 42161, Payee: "0x" + strings.Repeat("b", 40), URL: "https://" + name + ".example"}
		if err := st.RegisterRevenueSource(source, 100, "initial topology"); err != nil {
			t.Fatal(err)
		}
		names = append(names, source.SourceID)
		remotes[source.SourceID] = &broker{policy: regionalterms.Policy{PoolID: st.PoolID(), SourceID: source.SourceID, BrokerID: name, Paused: true}}
	}
	factory := func(source revenue.Source) (Broker, error) { return remotes[source.SourceID], nil }
	svc := &Service{Repo: st, Broker: factory}
	term := types.RegionalTerms{PoolID: st.PoolID(), Version: "v1", EffectiveRound: 100, WindowRounds: 14, CommissionBPS: 1000, ParticipationRules: "regional terms", ZeroWorkToOperator: true, RoundingToOperator: true}
	remotes[names[1]].unavailable = true
	if err := svc.Publish(context.Background(), term, "operator", "initial pool policy"); err == nil {
		t.Fatal("offline broker did not hold publication")
	}
	for _, b := range remotes {
		if b.activations != 0 {
			t.Fatal("activation began before all pause acknowledgements")
		}
	}
	published, _ := st.ListRegionalTerms()
	if len(published) != 0 {
		t.Fatal("partial terms published")
	}
	transactions, err := st.TermsPublications()
	if err != nil || len(transactions) != 1 || transactions[0].LastError == "" {
		t.Fatalf("hold not durable %+v %v", transactions, err)
	}
	remotes[names[1]].unavailable = false
	remotes[names[1]].loseActivation = true
	if err := svc.Resume(context.Background()); err == nil {
		t.Fatal("lost activation reply not held")
	}
	published, _ = st.ListRegionalTerms()
	if len(published) != 0 {
		t.Fatal("unconfirmed activation published")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = repo.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc = &Service{Repo: st, Broker: factory}
	if err := svc.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Publish(context.Background(), term, "operator", "initial pool policy"); err != nil {
		t.Fatal(err)
	}
	published, err = st.ListRegionalTerms()
	if err != nil || len(published) != 1 || published[0].Version != "v1" {
		t.Fatalf("publication %+v %v", published, err)
	}
	for name, b := range remotes {
		if b.activations != 1 || b.pauses != 1 {
			t.Fatalf("%s duplicated transition: %+v", name, b)
		}
	}
	events, err := st.ListAuditEvents()
	if err != nil || len(events) != 1 {
		t.Fatalf("audit repeated %d %v", len(events), err)
	}
	term.CommissionBPS = 2000
	if err := svc.Publish(context.Background(), term, "operator", "rewrite"); err == nil {
		t.Fatal("published version changed")
	}
}

func TestUnpublishedPartialActivationCanBeSupersededWithoutRewritingTerms(t *testing.T) {
	st, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	source := revenue.Source{PoolID: st.PoolID(), SourceID: "0x" + strings.Repeat("a", 64), BrokerID: "transcode-broker", ChainID: 42161, Payee: "0x" + strings.Repeat("b", 40), URL: "https://broker.example"}
	if err := st.RegisterRevenueSource(source, 100, "initial source"); err != nil {
		t.Fatal(err)
	}
	remote := &broker{policy: regionalterms.Policy{PoolID: st.PoolID(), SourceID: source.SourceID, BrokerID: source.BrokerID, Paused: true}}
	svc := &Service{Repo: st, Broker: func(revenue.Source) (Broker, error) { return remote, nil }}
	term := types.RegionalTerms{PoolID: st.PoolID(), Version: "v1", EffectiveRound: 100, WindowRounds: 14, CommissionBPS: 1000, ParticipationRules: "regional terms", ZeroWorkToOperator: true, RoundingToOperator: true}
	if err := svc.Publish(context.Background(), term, "operator", "initial"); err != nil {
		t.Fatal(err)
	}
	term.Version = "v2"
	term.EffectiveRound = 114
	term.CommissionBPS = 2000
	remote.loseActivation = true
	if err := svc.Publish(context.Background(), term, "operator", "next commission"); err == nil {
		t.Fatal("lost activation reply not held")
	}
	term.Version = "v3"
	term.EffectiveRound = 128
	if err := svc.Publish(context.Background(), term, "operator", "later boundary"); err == nil {
		t.Fatal("pending proposal implicitly replaced")
	}
	if err := svc.Publish(context.Background(), term, "operator", "later boundary", "v2"); err != nil {
		t.Fatal(err)
	}
	published, err := st.ListRegionalTerms()
	if err != nil || len(published) != 2 || published[0].Version != "v1" || published[1].Version != "v3" || published[0].CommissionBPS != 1000 {
		t.Fatalf("published history changed %+v %v", published, err)
	}
	records, _ := st.TermsPublications()
	if len(records) != 3 || records[1].State != "superseded" || records[1].SupersededBy != "v3" || records[2].State != "published" {
		t.Fatalf("supersession audit %+v", records)
	}
	if err := svc.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	if remote.policy.Version != "v3" || remote.pauses != 3 || remote.activations != 3 {
		t.Fatalf("old proposal resumed %+v", remote)
	}
	term.Version = "v4"
	term.EffectiveRound = 142
	if err := svc.Publish(context.Background(), term, "operator", "invalid rewrite", "v1"); err == nil {
		t.Fatal("published terms superseded")
	}
}
