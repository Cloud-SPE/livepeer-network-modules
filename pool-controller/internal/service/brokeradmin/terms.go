package brokeradmin

import (
	"context"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/regionalterms"
	"net/http"
)

type TermsPolicyRequest struct {
	PoolID           string `json:"pool_id"`
	ExpectedRevision uint64 `json:"expected_revision"`
	Action           string `json:"action"`
	Version          string `json:"version,omitempty"`
	EffectiveRound   uint64 `json:"effective_round,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

func (c *Client) TermsPolicy(ctx context.Context) (regionalterms.Policy, error) {
	var policy regionalterms.Policy
	err := c.doJSON(ctx, http.MethodGet, "/admin/v1/terms-policy", nil, &policy)
	return policy, err
}
func (c *Client) UpdateTermsPolicy(ctx context.Context, req TermsPolicyRequest) (regionalterms.Policy, error) {
	var policy regionalterms.Policy
	err := c.doJSON(ctx, http.MethodPost, "/admin/v1/terms-policy", req, &policy)
	return policy, err
}
