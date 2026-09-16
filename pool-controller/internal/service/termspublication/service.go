// Package termspublication coordinates a durable admission barrier across every
// regional broker before publishing an economic terms transition.
package termspublication

import (
	"context"
	"fmt"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/regionalterms"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type Broker interface {
	TermsPolicy(context.Context) (regionalterms.Policy, error)
	UpdateTermsPolicy(context.Context, brokeradmin.TermsPolicyRequest) (regionalterms.Policy, error)
}
type Service struct {
	Repo   *repo.StateRepo
	Broker func(revenue.Source) (Broker, error)
}

func (s *Service) Publish(ctx context.Context, terms types.RegionalTerms, actor, reason string, supersedes ...string) error {
	return s.Repo.WithTermsPublicationLock(func() error {
		sources, err := s.Repo.RevenueSources(nil)
		if err != nil {
			return err
		}
		existing, err := s.Repo.ListRegionalTerms()
		if err != nil {
			return err
		}
		if terms.WindowRounds == 0 {
			terms.WindowRounds = 14
		}
		if len(existing) > 0 {
			last := existing[len(existing)-1]
			if terms.Version != last.Version && (terms.EffectiveRound <= last.EffectiveRound || (terms.EffectiveRound-last.EffectiveRound)%last.WindowRounds != 0) {
				return fmt.Errorf("terms must append at an existing window boundary")
			}
		}
		item := repo.TermsPublication{Terms: terms, Actor: actor, Reason: reason}
		if len(supersedes) > 0 {
			item.SupersedesVersion = supersedes[0]
		}
		var first int64 = -1
		for _, source := range sources {
			if source.State == "retired" {
				continue
			}
			if first < 0 || source.StartRound < first {
				first = source.StartRound
			}
			if _, err := s.Broker(source.Source); err != nil {
				return err
			}
			item.Targets = append(item.Targets, repo.TermsPublicationTarget{Source: source.Source})
		}
		if len(existing) == 0 && (first < 0 || terms.EffectiveRound != uint64(first)) {
			return fmt.Errorf("initial terms must start with the first regional source round")
		}
		item, err = s.Repo.BeginTermsPublication(item)
		if err != nil {
			return err
		}
		return s.resume(ctx, item)
	})
}
func (s *Service) Resume(ctx context.Context) error {
	return s.Repo.WithTermsPublicationLock(func() error {
		items, err := s.Repo.TermsPublications()
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.State != "published" && item.State != "superseded" {
				if err := s.resume(ctx, item); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
func qualified(policy regionalterms.Policy, source revenue.Source) bool {
	return policy.PoolID == source.PoolID && policy.SourceID == source.SourceID && policy.BrokerID == source.BrokerID
}
func (s *Service) resume(ctx context.Context, item repo.TermsPublication) error {
	if item.State == "published" || item.State == "superseded" {
		return nil
	}
	fail := func(err error) error {
		item.LastError = err.Error()
		if saveErr := s.Repo.SaveTermsPublication(item); saveErr != nil {
			return fmt.Errorf("%v; save hold: %w", err, saveErr)
		}
		return err
	}
	// Every expected source must remain represented. A configuration edit cannot
	// silently drop an obligation from an in-progress transition.
	sources, err := s.Repo.RevenueSources(nil)
	if err != nil {
		return fail(err)
	}
	current := map[string]revenue.Source{}
	for _, source := range sources {
		if source.State != "retired" {
			current[source.Source.SourceID] = source.Source
		}
	}
	if len(current) != len(item.Targets) {
		return fail(fmt.Errorf("regional source set changed during terms publication"))
	}
	for _, target := range item.Targets {
		source, ok := current[target.Source.SourceID]
		if !ok || source.BrokerID != target.Source.BrokerID || source.URL != target.Source.URL {
			return fail(fmt.Errorf("terms source identity changed"))
		}
	}
	var failures []string
	for i := range item.Targets {
		target := &item.Targets[i]
		if target.Phase == "paused" || target.Phase == "activated" {
			continue
		}
		client, err := s.Broker(target.Source)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if target.Phase == "" {
			policy, err := client.TermsPolicy(ctx)
			if err != nil || !qualified(policy, target.Source) {
				failures = append(failures, fmt.Sprintf("%s: terms policy unavailable or wrong source: %v", target.Source.BrokerID, err))
				continue
			}
			target.ExpectedRevision = policy.Revision
			target.Phase = "queried"
			if err := s.Repo.SaveTermsPublication(item); err != nil {
				return err
			}
		}
		policy, err := client.UpdateTermsPolicy(ctx, brokeradmin.TermsPolicyRequest{PoolID: item.Terms.PoolID, Action: "pause", ExpectedRevision: target.ExpectedRevision, Reason: "terms publication " + item.Terms.Version + ": " + item.Reason})
		if err != nil || !qualified(policy, target.Source) || !policy.Paused || policy.Revision != target.ExpectedRevision+1 {
			failures = append(failures, fmt.Sprintf("%s: pause not confirmed: %v", target.Source.BrokerID, err))
			continue
		}
		target.Policy = policy
		target.Phase = "paused"
		if err := s.Repo.SaveTermsPublication(item); err != nil {
			return err
		}
	}
	if len(failures) > 0 {
		return fail(fmt.Errorf("terms admission barrier incomplete: %s", strings.Join(failures, "; ")))
	}
	for i := range item.Targets {
		target := &item.Targets[i]
		if target.Phase == "activated" {
			continue
		}
		client, err := s.Broker(target.Source)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		policy, err := client.UpdateTermsPolicy(ctx, brokeradmin.TermsPolicyRequest{PoolID: item.Terms.PoolID, Action: "activate", ExpectedRevision: target.Policy.Revision, Version: item.Terms.Version, EffectiveRound: item.Terms.EffectiveRound})
		if err != nil || !qualified(policy, target.Source) || policy.Paused || policy.Version != item.Terms.Version || policy.EffectiveRound != item.Terms.EffectiveRound || policy.Revision != target.Policy.Revision+1 {
			failures = append(failures, fmt.Sprintf("%s: settled terms activation not confirmed: %v", target.Source.BrokerID, err))
			continue
		}
		target.Policy = policy
		target.Phase = "activated"
		if err := s.Repo.SaveTermsPublication(item); err != nil {
			return err
		}
	}
	if len(failures) > 0 {
		return fail(fmt.Errorf("terms transition awaiting settled work: %s", strings.Join(failures, "; ")))
	}
	if err := s.Repo.PutRegionalTerms(item.Terms); err != nil {
		return fail(err)
	}
	if err := s.Repo.AppendAuditEvent(types.AuditEvent{ID: "terms-publication-" + item.Terms.Version, Kind: "regional_terms_published", Actor: item.Actor, ResourceID: item.Terms.Version, ResourceType: "regional_terms", Details: map[string]any{"pool_id": item.Terms.PoolID, "reason": item.Reason, "effective_round": item.Terms.EffectiveRound}}); err != nil {
		return fail(err)
	}
	item.State = "published"
	item.LastError = ""
	return s.Repo.SaveTermsPublication(item)
}
