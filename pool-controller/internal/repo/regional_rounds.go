package repo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	bolt "go.etcd.io/bbolt"
)

// SaveRegionalRound validates the entire required source/receipt set in the same
// transaction that freezes the regional round. No partial or mixed snapshot can
// become a payout input, including an apparently empty work set.
func (r *StateRepo) SaveRegionalRound(receipt types.RoundReceipt) error {
	round, err := strconv.ParseInt(receipt.RoundID, 10, 64)
	if err != nil || round < 0 || receipt.RoundID != strconv.FormatInt(round, 10) || receipt.PoolID != r.PoolID() || receipt.ID == "" {
		return fmt.Errorf("invalid regional round identity")
	}
	if receipt.PoolCutWei != "0" || receipt.DistributableWei != receipt.PoolRevenueWei || len(receipt.MemberPayouts) != 0 {
		return fmt.Errorf("regional rounds cannot allocate payouts before window close")
	}
	var conflict bool
	err = r.db.Update(func(tx *bolt.Tx) error {
		index, err := tx.CreateBucketIfNotExists([]byte("regional_round_numbers"))
		if err != nil {
			return err
		}
		if priorID := index.Get([]byte(receipt.RoundID)); priorID != nil {
			var prior types.RoundReceipt
			if err := json.Unmarshal(tx.Bucket([]byte(roundReceiptsBucket)).Get(priorID), &prior); err != nil {
				return err
			}
			if !sameRegionalRound(prior, receipt) {
				conflict = true
				return recordRegionalCorrection(tx, prior, receipt)
			}
			return nil
		}
		if publications := tx.Bucket([]byte("terms_publications")); publications != nil {
			if err := publications.ForEach(func(_, raw []byte) error {
				var pending TermsPublication
				if err := json.Unmarshal(raw, &pending); err != nil {
					return err
				}
				if pending.State != "published" && pending.State != "superseded" && pending.Terms.EffectiveRound <= uint64(round) {
					return fmt.Errorf("terms publication %s must finish before closing its rounds", pending.Terms.Version)
				}
				return nil
			}); err != nil {
				return err
			}
		}
		sources := map[string]RevenueSource{}
		bucket := tx.Bucket([]byte(revenueSourcesBucket))
		if bucket == nil {
			return fmt.Errorf("no source registry")
		}
		if err := bucket.ForEach(func(_, raw []byte) error {
			var source RevenueSource
			if err := json.Unmarshal(raw, &source); err != nil {
				return err
			}
			if source.StartRound <= round && (source.RetiredAfterRound == nil || *source.RetiredAfterRound >= round) {
				sources[source.Source.SourceID] = source
			}
			return nil
		}); err != nil {
			return err
		}
		if len(sources) == 0 || len(receipt.RevenueReports) != len(sources) || len(receipt.WorkReports) != len(sources) {
			return fmt.Errorf("required source evidence missing")
		}
		reports := map[string]revenue.Report{}
		workReports := map[string]revenue.WorkReport{}
		total := new(big.Int)
		now := time.Now().UTC()
		for _, report := range receipt.RevenueReports {
			source, ok := sources[report.SourceID]
			if !ok {
				return fmt.Errorf("unexpected revenue source")
			}
			if _, exists := reports[report.SourceID]; exists {
				return fmt.Errorf("duplicate revenue source")
			}
			if err := report.Validate(source.Source, round, now); err != nil {
				return err
			}
			reports[report.SourceID] = report
			amount, _ := new(big.Int).SetString(report.RevenueWei, 10)
			total.Add(total, amount)
		}
		if total.String() != receipt.PoolRevenueWei {
			return fmt.Errorf("regional revenue differs from complete source sum")
		}
		for _, report := range receipt.WorkReports {
			source, ok := sources[report.SourceID]
			if !ok {
				return fmt.Errorf("unexpected work source")
			}
			if _, exists := workReports[report.SourceID]; exists {
				return fmt.Errorf("duplicate work source")
			}
			if err := report.Validate(source.Source, round, now); err != nil {
				return err
			}
			workReports[report.SourceID] = report
		}
		included := map[string]bool{}
		for _, id := range receipt.IncludedWorkReceiptIDs {
			if included[id] {
				return fmt.Errorf("duplicate included receipt")
			}
			included[id] = true
		}
		digest := sha256.New()
		bySource := map[string][]revenue.Contribution{}
		count := 0
		if err := tx.Bucket([]byte(workReceiptsBucket)).ForEach(func(_, raw []byte) error {
			var work types.WorkReceipt
			if err := json.Unmarshal(raw, &work); err != nil {
				return err
			}
			if work.RoundID != receipt.RoundID || work.Status != "final" {
				return nil
			}
			_, _ = digest.Write(raw)
			_, _ = digest.Write([]byte{0})
			count++
			if !included[work.ID] || work.PoolID != r.PoolID() {
				return fmt.Errorf("receipt set incomplete or wrong pool")
			}
			if _, ok := sources[work.SourceID]; !ok {
				return fmt.Errorf("receipt source not registered for round")
			}
			amount, ok := new(big.Int).SetString(work.AttributedRevenueWei, 10)
			if !ok || amount.Sign() < 0 || work.MemberEthAddress == "" || work.OfferingID == "" || work.BackendID == "" {
				return fmt.Errorf("receipt lacks accepted billed-work attribution")
			}
			bySource[work.SourceID] = append(bySource[work.SourceID], revenue.Contribution{TermsVersion: work.TermsVersion, ID: work.ID, PoolID: work.PoolID, SourceID: work.SourceID, RoundID: work.RoundID, Member: work.MemberEthAddress, Offering: work.OfferingID, Backend: work.BackendID, AmountWei: amount.String()})
			return nil
		}); err != nil {
			return err
		}
		if count != len(included) || hex.EncodeToString(digest.Sum(nil)) != receipt.ReceiptSnapshot {
			return fmt.Errorf("receipt snapshot changed or incomplete")
		}
		for source, proof := range workReports {
			hash, err := revenue.WorkDigest(bySource[source])
			if err != nil {
				return err
			}
			if proof.ReceiptCount != uint64(len(bySource[source])) || proof.ReceiptDigest != hash {
				return fmt.Errorf("source %s receipt coverage incomplete", source)
			}
		}
		if receipt.CreatedAt.IsZero() {
			receipt.CreatedAt = now
		}
		raw, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		if err := tx.Bucket([]byte(roundReceiptsBucket)).Put([]byte(receipt.ID), raw); err != nil {
			return err
		}
		return index.Put([]byte(receipt.RoundID), []byte(receipt.ID))
	})
	if err != nil {
		return err
	}
	if conflict {
		return fmt.Errorf("closed regional round is immutable; correction evidence held for manual review")
	}
	return nil
}

func sameRegionalRound(a, b types.RoundReceipt) bool {
	a.CreatedAt = time.Time{}
	b.CreatedAt = time.Time{}
	// Preserve the accepted source observations; retries may carry newer freshness
	// stamps for the same immutable accounting snapshot.
	normalize := func(r *types.RoundReceipt) {
		r.RevenueReports = append([]revenue.Report(nil), r.RevenueReports...)
		for i := range r.RevenueReports {
			p := &r.RevenueReports[i]
			p.ObservedAt = time.Time{}
			p.ObservedHead = 0
			p.ObservedHeadHash = ""
			p.FinalizedBlock = 0
			p.FinalizedBlockHash = ""
			p.CompleteThroughRound = 0
		}
		sort.Slice(r.RevenueReports, func(i, j int) bool { return r.RevenueReports[i].SourceID < r.RevenueReports[j].SourceID })
		r.WorkReports = append([]revenue.WorkReport(nil), r.WorkReports...)
		for i := range r.WorkReports {
			r.WorkReports[i].ObservedAt = time.Time{}
			r.WorkReports[i].ClosedThroughRound = 0
		}
		sort.Slice(r.WorkReports, func(i, j int) bool { return r.WorkReports[i].SourceID < r.WorkReports[j].SourceID })
	}
	normalize(&a)
	normalize(&b)
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
