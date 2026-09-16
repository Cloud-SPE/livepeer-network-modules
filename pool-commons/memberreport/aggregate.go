package memberreport

import (
	"fmt"
	"math/big"
	"sort"
	"time"
)

type RegionalResult struct {
	PoolID    string    `json:"pool_id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"` // fresh, stale, unavailable; never inferred zero
	FetchedAt time.Time `json:"fetched_at"`
	Report    *Report   `json:"report,omitempty"`
}
type Contribution struct {
	PoolID   string `json:"pool_id"`
	WindowID string `json:"window_id"`
	Status   string `json:"status"`
}
type Aggregate struct {
	ChainID             uint64         `json:"chain_id"`
	Asset               string         `json:"asset"`
	StartRound          uint64         `json:"start_round"`
	EndRound            uint64         `json:"end_round"`
	Complete            bool           `json:"complete"`
	Sources             []Contribution `json:"sources"`
	MissingPools        []string       `json:"missing_pools"`
	EarnedWei           string         `json:"earned_wei"`
	AwaitingApprovalWei string         `json:"awaiting_approval_wei"`
	PendingWei          string         `json:"pending_wei"`
	PaidWei             string         `json:"paid_wei"`
}

func number(raw string) (*big.Int, error) {
	n, ok := new(big.Int).SetString(raw, 10)
	if !ok || n.Sign() < 0 || n.String() != raw {
		return nil, fmt.Errorf("invalid exact monetary value")
	}
	return n, nil
}

// Validate rejects mismatched attribution and inconsistent categories before caching.
func Validate(report Report, pool, wallet string) error {
	if report.PoolID != pool || report.Wallet != wallet || report.ObservedAt.IsZero() {
		return fmt.Errorf("regional report identity or observation missing")
	}
	ids := map[string]bool{}
	periods := map[string]bool{}
	for _, w := range report.Windows {
		period := fmt.Sprintf("%d/%s/%d/%d", w.ChainID, w.Asset, w.StartRound, w.EndRound)
		if w.PoolID != pool || w.WindowID == "" || ids[w.WindowID] || periods[period] || w.ChainID == 0 || w.Asset == "" || w.StartRound > w.EndRound || w.TermsVersion == "" || len(w.Sources) == 0 {
			return fmt.Errorf("invalid qualified regional window")
		}
		ids[w.WindowID] = true
		periods[period] = true
		earned, err := number(w.EarnedWei)
		if err != nil {
			return err
		}
		total := new(big.Int)
		for _, raw := range []string{w.AwaitingApprovalWei, w.PendingWei, w.PaidWei} {
			n, err := number(raw)
			if err != nil {
				return err
			}
			total.Add(total, n)
		}
		if total.Cmp(earned) != 0 {
			return fmt.Errorf("regional categories do not conserve earned amount")
		}
		for _, source := range w.Sources {
			if source.PoolID != pool || source.SourceID == "" || source.Round < w.StartRound || source.Round > w.EndRound || source.InclusionDigest == "" || source.WorkDigest == "" {
				return fmt.Errorf("regional source attribution incomplete")
			}
		}
		for _, payment := range w.Payments {
			if payment.PoolID != pool {
				return fmt.Errorf("payment pool mismatch")
			}
		}
	}
	return nil
}

// Combine groups only identical chain/asset/round intervals. A stale contribution
// stays visibly stale; an absent or nonmatching pool makes that group incomplete.
func Combine(regions []RegionalResult) ([]Aggregate, error) {
	groups := map[string]*Aggregate{}
	wallet := ""
	pools := map[string]bool{}
	for _, region := range regions {
		if region.PoolID == "" || pools[region.PoolID] {
			return nil, fmt.Errorf("duplicate or missing pool")
		}
		pools[region.PoolID] = true
		if region.Status != "fresh" && region.Status != "stale" && region.Status != "unavailable" {
			return nil, fmt.Errorf("invalid freshness")
		}
		if region.Report == nil {
			if region.Status != "unavailable" {
				return nil, fmt.Errorf("missing regional report")
			}
			continue
		}
		if region.Status == "unavailable" {
			return nil, fmt.Errorf("unavailable report must not carry amounts")
		}
		if wallet == "" {
			wallet = region.Report.Wallet
		}
		if region.Report.Wallet != wallet {
			return nil, fmt.Errorf("cannot combine different member wallets")
		}
		if err := Validate(*region.Report, region.PoolID, region.Report.Wallet); err != nil {
			return nil, err
		}
		for _, w := range region.Report.Windows {
			key := fmt.Sprintf("%d/%s/%020d/%020d", w.ChainID, w.Asset, w.StartRound, w.EndRound)
			g := groups[key]
			if g == nil {
				g = &Aggregate{ChainID: w.ChainID, Asset: w.Asset, StartRound: w.StartRound, EndRound: w.EndRound, Complete: true, Sources: []Contribution{}, MissingPools: []string{}, EarnedWei: "0", AwaitingApprovalWei: "0", PendingWei: "0", PaidWei: "0"}
				groups[key] = g
			}
			g.Sources = append(g.Sources, Contribution{PoolID: region.PoolID, WindowID: w.WindowID, Status: region.Status})
			if region.Status != "fresh" {
				g.Complete = false
			}
			for _, pair := range []struct {
				dst *string
				src string
			}{{&g.EarnedWei, w.EarnedWei}, {&g.AwaitingApprovalWei, w.AwaitingApprovalWei}, {&g.PendingWei, w.PendingWei}, {&g.PaidWei, w.PaidWei}} {
				a, _ := number(*pair.dst)
				b, _ := number(pair.src)
				*pair.dst = a.Add(a, b).String()
			}
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]Aggregate, 0, len(keys))
	for _, key := range keys {
		g := groups[key]
		present := map[string]bool{}
		for _, source := range g.Sources {
			present[source.PoolID] = true
		}
		for pool := range pools {
			if !present[pool] {
				g.MissingPools = append(g.MissingPools, pool)
				g.Complete = false
			}
		}
		sort.Strings(g.MissingPools)
		sort.Slice(g.Sources, func(i, j int) bool { return g.Sources[i].PoolID < g.Sources[j].PoolID })
		out = append(out, *g)
	}
	return out, nil
}
