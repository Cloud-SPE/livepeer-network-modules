package main

import (
	"context"
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/poolcontroller"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/types"
	"math/big"
	"strconv"
)

func prepareRegionalClose(ctx context.Context, cfg *config.Config, client *poolcontroller.Client, req types.RoundCloseRequest) (types.RoundCloseRequest, error) {
	round, err := strconv.ParseInt(req.RoundID, 10, 64)
	if err != nil {
		return req, err
	}
	sources, err := client.RevenueSources(ctx, cfg.PoolController.PoolID, round)
	if err != nil {
		return req, err
	}
	configured := map[string]revenue.Source{}
	for _, source := range cfg.RevenueSources {
		if _, exists := configured[source.SourceID]; exists {
			return req, fmt.Errorf("duplicate configured source %s", source.SourceID)
		}
		configured[source.SourceID] = source
	}
	req.RevenueReports = nil
	req.WorkReports = nil
	total := new(big.Int)
	for _, source := range sources {
		local, ok := configured[source.SourceID]
		if !ok {
			return req, fmt.Errorf("source %s round %d: historical source credentials unavailable", source.SourceID, round)
		}
		localPublic := local
		localPublic.TokenFile = ""
		localPublic.CAFile = ""
		if localPublic != source {
			return req, fmt.Errorf("source %s registry differs from configured identity/origin", source.SourceID)
		}
		report, err := revenue.Collect(ctx, local, round)
		if err != nil {
			return req, fmt.Errorf("source %s round %d: %w", source.SourceID, round, err)
		}
		req.RevenueReports = append(req.RevenueReports, report)
		work, err := revenue.CollectWork(ctx, local, round)
		if err != nil {
			return req, fmt.Errorf("source %s work round %d: %w", source.SourceID, round, err)
		}
		req.WorkReports = append(req.WorkReports, work)
		value, _ := new(big.Int).SetString(report.RevenueWei, 10)
		total.Add(total, value)
	}
	receipts, snapshot, err := client.ListAllWorkReceipts(ctx, req.RoundID, "final")
	if err != nil {
		return req, err
	}
	req.ReceiptSnapshot = snapshot
	req.PoolID = cfg.PoolController.PoolID
	req.IncludedWorkReceiptIDs = []string{}
	contributions := map[string][]revenue.Contribution{}
	for _, receipt := range receipts {
		if receipt.PoolID != req.PoolID || receipt.SourceID == "" || receipt.RoundID != req.RoundID || receipt.Status != "final" {
			return req, fmt.Errorf("receipt %s lacks regional source attribution", receipt.ID)
		}
		found := false
		for _, source := range sources {
			if source.SourceID == receipt.SourceID {
				found = true
				break
			}
		}
		if !found {
			return req, fmt.Errorf("receipt %s names an unexpected receiver source", receipt.ID)
		}
		contributions[receipt.SourceID] = append(contributions[receipt.SourceID], revenue.Contribution{TermsVersion: receipt.TermsVersion, ID: receipt.ID, PoolID: receipt.PoolID, SourceID: receipt.SourceID, RoundID: receipt.RoundID, Member: receipt.MemberEthAddress, Offering: receipt.OfferingID, Backend: receipt.BackendID, AmountWei: receipt.AttributedRevenueWei})
		req.IncludedWorkReceiptIDs = append(req.IncludedWorkReceiptIDs, receipt.ID)
	}
	for _, proof := range req.WorkReports {
		items := contributions[proof.SourceID]
		digest, err := revenue.WorkDigest(items)
		if err != nil || digest != proof.ReceiptDigest || uint64(len(items)) != proof.ReceiptCount {
			return req, fmt.Errorf("source %s complete receipt set has not arrived", proof.SourceID)
		}
	}
	req.PoolRevenueWei = total.String()
	req.PoolCutWei = "0" // commission is applied once at regional window close
	return req, nil
}
