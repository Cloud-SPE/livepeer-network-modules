package types

type RoundCloseRequest struct {
	ID                     string   `json:"id"`
	RoundID                string   `json:"round_id"`
	PoolRevenueWei         string   `json:"pool_revenue_wei"`
	PoolCutWei             string   `json:"pool_cut_wei"`
	IncludedWorkReceiptIDs []string `json:"included_work_receipt_ids"`
}
