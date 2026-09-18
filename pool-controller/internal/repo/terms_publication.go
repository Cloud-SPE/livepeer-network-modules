package repo

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/regionalterms"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	bolt "go.etcd.io/bbolt"
)

type TermsPublicationTarget struct {
	Source           revenue.Source       `json:"source"`
	Phase            string               `json:"phase"`
	ExpectedRevision uint64               `json:"expected_revision"`
	Policy           regionalterms.Policy `json:"policy"`
}
type TermsPublication struct {
	SupersedesVersion string                   `json:"supersedes_version,omitempty"`
	SupersededBy      string                   `json:"superseded_by,omitempty"`
	Terms             types.RegionalTerms      `json:"terms"`
	Actor             string                   `json:"actor"`
	Reason            string                   `json:"reason"`
	State             string                   `json:"state"`
	Targets           []TermsPublicationTarget `json:"targets"`
	LastError         string                   `json:"last_error,omitempty"`
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

func (r *StateRepo) BeginTermsPublication(item TermsPublication) (TermsPublication, error) {
	var result TermsPublication
	if item.Terms.PoolID != r.PoolID() || item.Terms.Version == "" || len(item.Terms.Version) > 128 || strings.ContainsAny(item.Terms.Version, "\x00/\n") || item.Actor == "" || item.Reason == "" || len(item.Targets) == 0 {
		return result, fmt.Errorf("terms publication requires pool, version, actor, reason and source targets")
	}
	if item.Terms.WindowRounds == 0 {
		item.Terms.WindowRounds = 14
	}
	if item.Terms.CommissionBPS > 10000 || item.Terms.ParticipationRules == "" || !item.Terms.ZeroWorkToOperator || !item.Terms.RoundingToOperator {
		return result, fmt.Errorf("invalid regional terms")
	}
	err := r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("terms_publications"))
		if raw := b.Get([]byte(item.Terms.Version)); raw != nil {
			if err := json.Unmarshal(raw, &result); err != nil {
				return err
			}
			if !sameJSON(result.Terms, item.Terms) || result.SupersedesVersion != item.SupersedesVersion {
				return fmt.Errorf("terms publication version is immutable")
			}
			return nil
		}
		if tx.Bucket([]byte(regionalTermsBucket)).Get([]byte(item.Terms.Version)) != nil {
			return fmt.Errorf("terms version already published")
		}
		var superseded *TermsPublication
		if err := b.ForEach(func(_, raw []byte) error {
			var prior TermsPublication
			if err := json.Unmarshal(raw, &prior); err != nil {
				return err
			}
			if prior.State == "published" || prior.State == "superseded" {
				return nil
			}
			if item.SupersedesVersion != prior.Terms.Version {
				return fmt.Errorf("another terms publication remains unresolved; name it explicitly to supersede")
			}
			if tx.Bucket([]byte(regionalTermsBucket)).Get([]byte(prior.Terms.Version)) != nil {
				return fmt.Errorf("published terms cannot be superseded")
			}
			if item.Terms.EffectiveRound <= prior.Terms.EffectiveRound {
				return fmt.Errorf("superseding terms require a later activation boundary")
			}
			prior.State = "superseded"
			prior.SupersededBy = item.Terms.Version
			prior.UpdatedAt = time.Now().UTC()
			superseded = &prior
			return nil
		}); err != nil {
			return err
		}
		if item.SupersedesVersion != "" && superseded == nil {
			return fmt.Errorf("unresolved unpublished terms proposal not found")
		}
		if superseded != nil {
			raw, err := json.Marshal(superseded)
			if err != nil {
				return err
			}
			if err := b.Put([]byte(superseded.Terms.Version), raw); err != nil {
				return err
			}
		}
		item.State = "pending"
		item.CreatedAt = time.Now().UTC()
		item.UpdatedAt = item.CreatedAt
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		result = item
		return b.Put([]byte(item.Terms.Version), raw)
	})
	return result, err
}
func (r *StateRepo) SaveTermsPublication(item TermsPublication) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("terms_publications"))
		var prior TermsPublication
		if err := json.Unmarshal(b.Get([]byte(item.Terms.Version)), &prior); err != nil {
			return err
		}
		if !sameJSON(prior.Terms, item.Terms) || prior.Actor != item.Actor || prior.SupersedesVersion != item.SupersedesVersion || prior.SupersededBy != item.SupersededBy || prior.Reason != item.Reason || len(prior.Targets) != len(item.Targets) {
			return fmt.Errorf("terms publication identity changed")
		}
		for i := range prior.Targets {
			if !sameJSON(prior.Targets[i].Source, item.Targets[i].Source) {
				return fmt.Errorf("terms publication source changed")
			}
		}
		if (prior.State == "published" || prior.State == "superseded") && !sameJSON(prior, item) {
			return fmt.Errorf("published terms transaction is immutable")
		}
		item.UpdatedAt = time.Now().UTC()
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		return b.Put([]byte(item.Terms.Version), raw)
	})
}
func (r *StateRepo) TermsPublications() ([]TermsPublication, error) {
	return listJSON(r, "terms_publications", func(a, b TermsPublication) bool { return a.Terms.EffectiveRound < b.Terms.EffectiveRound })
}

func (r *StateRepo) WithTermsPublicationLock(fn func() error) error {
	r.termsPublicationMu.Lock()
	defer r.termsPublicationMu.Unlock()
	return fn()
}
