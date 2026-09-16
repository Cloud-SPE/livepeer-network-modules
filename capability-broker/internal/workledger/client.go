package workledger

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/receipts"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

// Client records the retry obligation before calling the receiver. Its original
// sequence and bytes survive a lost response and normal process restart.
type Client struct {
	RequireTerms  bool
	admissionGate sync.RWMutex
	payment.Client
	payment.AccountClient
	Store  *Store
	Reader payment.RevenueReader
	Domain payment.DomainClient
	Sink   receipts.Client
}

func (c *Client) SettlementDomain(ctx context.Context) (string, error) {
	return c.Domain.SettlementDomain(ctx)
}
func (c *Client) RoundRevenue(ctx context.Context, round int64) (*pb.GetRoundRevenueResponse, error) {
	return c.Reader.RoundRevenue(ctx, round)
}
func (c *Client) AdmitAuthorization(ctx context.Context, req payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	c.admissionGate.RLock()
	defer c.admissionGate.RUnlock()
	draining, err := c.Store.Draining()
	if err != nil {
		return nil, err
	}
	var auth pb.SpendAuthorization
	if err := proto.Unmarshal(req.AuthorizationBytes, &auth); err != nil {
		return nil, err
	}
	if auth.GetPayload() == nil || auth.GetPayload().GetSettlementDomainId() != c.Store.SourceID || len(auth.GetPayload().GetPayer()) != 20 {
		return nil, fmt.Errorf("admission source identity invalid")
	}
	bindingID := auth.GetPayload().GetPredecessorAuthorizationId()
	if bindingID == "" {
		bindingID = auth.GetPayload().GetAuthorizationId()
	}
	deviceDraining, err := c.Store.authorizationDeviceDraining(bindingID)
	if err != nil {
		return nil, err
	}
	draining = draining || deviceDraining
	if c.RequireTerms {
		if err := c.Store.TermsPermitAdmission(); err != nil {
			draining = true
		}
	}
	if draining {
		known, err := c.Store.KnownBoundAuthorization(auth.GetPayload().GetAuthorizationId(), req.AuthorizationBytes)
		if err != nil {
			return nil, err
		}
		if !known {
			return nil, fmt.Errorf("%w: regional source no longer admits new work", payment.ErrAdmissionStopped)
		}
		accepted, err := c.AccountClient.GetSpendAuthorization(ctx, auth.GetPayload().GetPayer(), auth.GetPayload().GetAuthorizationId())
		if err != nil {
			return nil, err
		}
		if accepted == nil || accepted.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) {
			return nil, fmt.Errorf("draining source permits only already-admitted bound replay")
		}
	}
	if err := c.Store.SaveAuthorization(auth.GetPayload().GetAuthorizationId(), req.AuthorizationBytes); err != nil {
		return nil, err
	}
	// Copy an already trusted binding before successor admission. A failed
	// admission leaves an inert binding; it cannot fabricate billed work.
	if previous := auth.GetPayload().GetPredecessorAuthorizationId(); previous != "" {
		binding, err := c.Store.Binding(previous)
		if err != nil {
			return nil, err
		}
		if err = c.Store.Bind(auth.GetPayload().GetAuthorizationId(), binding); err != nil {
			return nil, err
		}
	}
	result, err := c.AccountClient.AdmitAuthorization(ctx, req)
	if err != nil {
		return nil, err
	}
	if previous := auth.GetPayload().GetPredecessorAuthorizationId(); previous != "" && result != nil && result.State == int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) {
		predecessor, err := c.AccountClient.GetSpendAuthorization(ctx, auth.GetPayload().GetPayer(), previous)
		if err != nil {
			return nil, err
		}
		if predecessor == nil || predecessor.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SUPERSEDED) {
			return nil, fmt.Errorf("receiver did not prove superseded billing baseline")
		}
		if err = c.Store.SetInheritedBaseline(auth.GetPayload().GetAuthorizationId(), previous, predecessor.Billed, predecessor.ActualUnits); err != nil {
			return nil, err
		}
	}
	return result, nil
}
func (c *Client) AdvanceAuthorization(ctx context.Context, req payment.AdvanceAuthorizationRequest) (*payment.AdvanceAuthorizationResult, error) {
	target := ""
	if req.TargetReserved != nil {
		target = req.TargetReserved.String()
	}
	op, err := c.Store.Prepare(Operation{AuthorizationID: req.AuthorizationID, Kind: "advance", Sequence: req.AdvanceSeq, Payer: req.Payer, Units: req.CumulativeUnits, TargetReserved: target, PaymentBytes: req.PaymentBytes})
	if err != nil {
		return nil, err
	}
	result, err := c.AccountClient.AdvanceAuthorization(ctx, req)
	if err != nil {
		return nil, err
	}
	if result == nil || result.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) {
		return nil, fmt.Errorf("receiver returned no valid billing result")
	}
	if err = c.Store.Complete(op.ID, result.CumulativeBilled); err != nil {
		return nil, err
	}
	return result, nil
}
func (c *Client) SettleAuthorization(ctx context.Context, req payment.SettleAuthorizationRequest) (*payment.SettleAuthorizationResult, error) {
	op, err := c.Store.Prepare(Operation{AuthorizationID: req.AuthorizationID, Kind: "settle", Sequence: req.SettlementSeq, Payer: req.Payer, Units: req.ActualUnits})
	if err != nil {
		return nil, err
	}
	result, err := c.AccountClient.SettleAuthorization(ctx, req)
	if err != nil {
		return nil, err
	}
	if result == nil || result.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED) {
		return nil, fmt.Errorf("receiver returned no valid settlement result")
	}
	if err = c.Store.Complete(op.ID, result.Billed); err != nil {
		return nil, err
	}
	return result, nil
}

// Flush recovers uncertain receiver results, finalizes against a fresh confirmed
// round observation, then delivers the durable outbox. Any failure holds proof
// of completeness; acknowledgements alone remove items from the delivery queue.
func (c *Client) Flush(ctx context.Context) error {
	pending, err := c.Store.Pending()
	if err != nil {
		return err
	}
	for _, op := range pending {
		var total *big.Int
		if op.Kind == "advance" {
			var target *big.Int
			if op.TargetReserved != "" {
				var ok bool
				target, ok = new(big.Int).SetString(op.TargetReserved, 10)
				if !ok {
					return fmt.Errorf("invalid persisted reservation")
				}
			}
			result, e := c.AccountClient.AdvanceAuthorization(ctx, payment.AdvanceAuthorizationRequest{Payer: op.Payer, AuthorizationID: op.AuthorizationID, CumulativeUnits: op.Units, TargetReserved: target, AdvanceSeq: op.Sequence, PaymentBytes: op.PaymentBytes})
			if e != nil {
				return e
			}
			if result == nil || result.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) {
				return fmt.Errorf("missing valid replay result")
			}
			total = result.CumulativeBilled
		} else {
			result, e := c.AccountClient.SettleAuthorization(ctx, payment.SettleAuthorizationRequest{Payer: op.Payer, AuthorizationID: op.AuthorizationID, ActualUnits: op.Units, SettlementSeq: op.Sequence})
			if e != nil {
				return e
			}
			if result == nil || result.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED) {
				return fmt.Errorf("missing valid replay result")
			}
			total = result.Billed
		}
		if err = c.Store.Complete(op.ID, total); err != nil {
			return err
		}
	}
	clock, err := c.Reader.RoundRevenue(ctx, 0)
	if err != nil {
		return err
	}
	if clock == nil || clock.SettlementDomainId != c.Store.SourceID || clock.CompleteThroughRound < 0 {
		return fmt.Errorf("receiver round observation unavailable")
	}
	observed, err := time.Parse(time.RFC3339Nano, clock.ObservedAt)
	now := time.Now()
	if err != nil || now.Sub(observed) > 2*time.Minute || observed.After(now.Add(time.Minute)) {
		return fmt.Errorf("receiver round observation stale")
	}
	if err = c.Store.Finalize(clock.CompleteThroughRound+1, observed); err != nil {
		return err
	}
	if c.Sink == nil {
		return fmt.Errorf("regional receipt sink unavailable")
	}
	batch, err := c.Store.Undelivered(500)
	if err != nil {
		return err
	}
	for _, receipt := range batch {
		if err = c.Sink.UpsertWorkReceipt(ctx, receipt); err != nil {
			return err
		}
		if err = c.Store.Delivered(receipt.ID); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) BeginDrain(reason string) error {
	c.admissionGate.Lock()
	defer c.admissionGate.Unlock()
	return c.Store.BeginDrain(reason)
}
