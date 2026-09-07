package settlementkeys

import (
	"sort"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/types"
)

// Sources, in precedence order. A key present in more than one keeps
// the highest source's window.
const (
	SourceConfig    = "config"
	SourceBroker    = "broker"
	SourcePublished = "published"

	WindowConfig    = "config"
	WindowBroker    = "broker"
	WindowDefault   = "default"
	WindowPublished = "published"
)

// DefaultValidity is the window a coordinator assigns to a key the
// broker announced unbounded, when publish.settlement_key_validity is
// unset.
const DefaultValidity = 365 * 24 * time.Hour

// BrokerKeys is what one broker announced, after verification.
type BrokerKeys struct {
	Name    string
	BaseURL string
	Keys    []Discovered
}

// MergeInput is everything the merge decides from.
type MergeInput struct {
	// Config is the operator's pinned list: keys the coordinator cannot
	// discover, or a window the operator chose. Wins over everything.
	Config []types.SettlementKey
	// Brokers is the discovery result. Only proven keys are delegated.
	Brokers []BrokerKeys
	// Published is the current manifest's list. A key still inside its
	// window stays delegated even when no broker announces it any more
	// — that is the rotation overlap, and it is also what keeps a
	// broker that is down at scrape time from silently losing its
	// delegation.
	Published []types.SettlementKey
	Now       time.Time
	Validity  time.Duration
	Windows   Windower
}

// Merge returns the candidate's settlement_keys, sorted by public_key,
// with the provenance of each. Expired keys are dropped from every
// source but config, where the operator wrote the window on purpose.
func Merge(in MergeInput) ([]types.SettlementKey, []types.MetadataSettlementKey) {
	now := in.Now.UTC()
	validity := in.Validity
	if validity <= 0 {
		validity = DefaultValidity
	}
	type entry struct {
		key  types.SettlementKey
		meta types.MetadataSettlementKey
	}
	chosen := map[string]entry{}
	put := func(e entry) {
		if _, taken := chosen[e.key.PublicKey]; taken {
			return
		}
		chosen[e.key.PublicKey] = e
	}

	for _, k := range in.Config {
		put(entry{
			key: normalizeKey(k),
			meta: types.MetadataSettlementKey{
				PublicKey: k.PublicKey, Source: SourceConfig, WindowSource: WindowConfig,
				NotBefore: k.NotBefore.UTC(), ExpiresAt: k.ExpiresAt.UTC(),
			},
		})
	}

	for _, b := range in.Brokers {
		for _, d := range b.Keys {
			if !d.Proven {
				continue
			}
			nb, exp, src := d.NotBefore, d.ExpiresAt, WindowBroker
			if nb.IsZero() || exp.IsZero() {
				if in.Windows == nil {
					continue
				}
				nb, exp = in.Windows.Window(d.PublicKey, now, validity)
				src = WindowDefault
			}
			if !exp.After(now) {
				continue
			}
			put(entry{
				key: types.SettlementKey{PublicKey: d.PublicKey, NotBefore: nb, ExpiresAt: exp},
				meta: types.MetadataSettlementKey{
					PublicKey: d.PublicKey, Source: SourceBroker, Broker: b.Name, BaseURL: b.BaseURL,
					WindowSource: src, NotBefore: nb, ExpiresAt: exp,
				},
			})
		}
	}

	for _, k := range in.Published {
		if !k.ExpiresAt.After(now) {
			continue
		}
		put(entry{
			key: normalizeKey(k),
			meta: types.MetadataSettlementKey{
				PublicKey: k.PublicKey, Source: SourcePublished, WindowSource: WindowPublished,
				NotBefore: k.NotBefore.UTC(), ExpiresAt: k.ExpiresAt.UTC(),
			},
		})
	}

	keys := make([]types.SettlementKey, 0, len(chosen))
	metas := make([]types.MetadataSettlementKey, 0, len(chosen))
	order := make([]string, 0, len(chosen))
	for pk := range chosen {
		order = append(order, pk)
	}
	sort.Strings(order)
	for _, pk := range order {
		keys = append(keys, chosen[pk].key)
		metas = append(metas, chosen[pk].meta)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	return keys, metas
}

func normalizeKey(k types.SettlementKey) types.SettlementKey {
	return types.SettlementKey{PublicKey: k.PublicKey, NotBefore: k.NotBefore.UTC(), ExpiresAt: k.ExpiresAt.UTC()}
}
