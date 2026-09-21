package workledger

import (
	"context"
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

// RecoverUnbound runs before accepting traffic. A receiver-side tombstone makes
// absence definitive even if the old admission RPC is still executing there.
// Bound work is deliberately excluded; its execution cannot be guessed away.
func (c *Client) RecoverUnbound(ctx context.Context) error {
	var entries [][]byte
	if err := c.Store.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("unbound_admissions")).ForEach(func(_, raw []byte) error { entries = append(entries, append([]byte(nil), raw...)); return nil })
	}); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	receiver, ok := c.AccountClient.(payment.UnexecutedRecovery)
	if !ok {
		return fmt.Errorf("receiver cannot fence unbound admissions")
	}
	for _, raw := range entries {
		var auth pb.SpendAuthorization
		if err := proto.Unmarshal(raw, &auth); err != nil {
			return err
		}
		payload := auth.GetPayload()
		if payload == nil || payload.GetAuthorizationId() == "" || len(payload.GetPayer()) != 20 || payload.GetSettlementDomainId() != c.Store.SourceID {
			return fmt.Errorf("unbound authorization source identity invalid")
		}
		if err := receiver.CloseUnexecutedAuthorization(ctx, payload.GetPayer(), payload.GetAuthorizationId(), "broker restart: durable admission has no runner binding", payload.GetWholesaleAccountId()); err != nil {
			return fmt.Errorf("unbound admission %s recovery held: %w", payload.GetAuthorizationId(), err)
		}
		if err := c.Store.db.Update(func(tx *bolt.Tx) error {
			if err := tx.Bucket([]byte("unbound_rounds")).Delete([]byte(payload.GetAuthorizationId())); err != nil {
				return err
			}
			return tx.Bucket([]byte("unbound_admissions")).Delete([]byte(payload.GetAuthorizationId()))
		}); err != nil {
			return err
		}
	}
	return nil
}
