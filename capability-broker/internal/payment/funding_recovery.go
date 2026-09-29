package payment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/identity"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

// FundingRecovery only reads an existing receipt; it must never process tickets.
type FundingRecovery interface {
	GetWholesaleFundingReceipt(context.Context, []byte, string) (*FundWholesaleAccountResult, error)
}

func (g *GRPC) GetWholesaleFundingReceipt(ctx context.Context, wire []byte, account string) (*FundWholesaleAccountResult, error) {
	if !identity.ValidWholesaleAccountID(account) {
		return nil, fmt.Errorf("invalid account")
	}
	var pay pb.Payment
	if err := proto.Unmarshal(wire, &pay); err != nil {
		return nil, err
	}
	domain, err := g.SettlementDomain(ctx)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(wire)
	id := hex.EncodeToString(digest[:])
	r, err := g.client.GetWholesaleFundingReceipt(ctx, &pb.GetWholesaleFundingReceiptRequest{Payer: pay.GetSender(), SettlementDomainId: domain, WholesaleAccountId: account, FundingId: id})
	if err != nil {
		return nil, err
	}
	if err = g.validateAccount(r.GetAccount(), pay.GetSender(), account); err != nil {
		return nil, err
	}
	if r.GetFundingId() != id || !r.GetReplayed() || !bytes.Equal(r.GetAccount().GetPayee(), pay.GetTicketParams().GetRecipient()) {
		return nil, fmt.Errorf("funding receipt scope mismatch")
	}
	return &FundWholesaleAccountResult{FundingID: id, Account: accountFromProto(r.GetAccount()), Credited: new(big.Int).SetBytes(r.GetCreditedValueWei().GetValue()), Replayed: true}, nil
}
func (m *metered) GetWholesaleFundingReceipt(ctx context.Context, wire []byte, account string) (*FundWholesaleAccountResult, error) {
	r, ok := m.inner.(FundingRecovery)
	if !ok {
		return nil, errors.ErrUnsupported
	}
	return r.GetWholesaleFundingReceipt(ctx, wire, account)
}
