package server

import (
	"context"
	"fmt"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
)

func (s *Server) bindPaymentLedger(store *sessionstore.Store) error {
	client, ok := s.payment.(payment.DomainClient)
	if !ok {
		return fmt.Errorf("payment client cannot identify its settlement domain")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := client.SettlementDomain(ctx)
	if err != nil {
		return err
	}
	return store.BindSettlementDomain(id)
}
