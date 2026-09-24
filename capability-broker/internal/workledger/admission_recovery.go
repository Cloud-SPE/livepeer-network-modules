package workledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

func (c *Client) CancelAuthorizationAdmission(ctx context.Context, wire []byte) (*payment.CanceledAdmission, error) {
	inner, ok := c.AccountClient.(payment.AdmissionRecovery)
	if !ok {
		return nil, errors.ErrUnsupported
	}
	result, err := inner.CancelAuthorizationAdmission(ctx, wire)
	if err != nil || result == nil || result.Canceled || result.Authorization == nil {
		return result, err
	}
	state := result.Authorization.State
	if state != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) && state != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED) {
		return result, nil
	}
	var auth pb.SpendAuthorization
	if err := proto.Unmarshal(wire, &auth); err != nil {
		return nil, err
	}
	p := auth.GetPayload()
	if p == nil || p.GetSettlementDomainId() != c.Store.SourceID {
		return nil, fmt.Errorf("admission recovery source identity invalid")
	}
	if previous := p.GetPredecessorAuthorizationId(); previous != "" {
		predecessor, err := c.AccountClient.GetSpendAuthorization(ctx, p.GetPayer(), previous)
		if err != nil {
			return nil, err
		}
		if predecessor == nil || predecessor.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SUPERSEDED) {
			return nil, fmt.Errorf("receiver did not prove superseded billing baseline")
		}
		if err := c.Store.SetInheritedBaseline(p.GetAuthorizationId(), previous, predecessor.Billed, predecessor.ActualUnits); err != nil {
			return nil, err
		}
	}
	return result, nil
}
