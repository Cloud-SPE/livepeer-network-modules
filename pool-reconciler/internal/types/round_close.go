package types

import "github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"

type RoundCloseRequest struct {
	WorkReports            []revenue.WorkReport `json:"work_reports,omitempty"`
	PoolID                 string               `json:"pool_id,omitempty"`
	RevenueReports         []revenue.Report     `json:"revenue_reports,omitempty"`
	ReceiptSnapshot        string               `json:"receipt_snapshot,omitempty"`
	ID                     string               `json:"id"`
	RoundID                string               `json:"round_id"`
	PoolRevenueWei         string               `json:"pool_revenue_wei"`
	PoolCutWei             string               `json:"pool_cut_wei"`
	IncludedWorkReceiptIDs []string             `json:"included_work_receipt_ids"`
}
