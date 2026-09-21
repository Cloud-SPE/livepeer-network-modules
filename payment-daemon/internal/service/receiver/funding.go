package receiver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/identity"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/receiver/validator"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/types"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// fundWholesale is also used by inline admission/advance. No generation balance
// is swept; validated tickets and their exact funding receipt commit atomically.
func (s *Service) fundWholesale(_ context.Context, req *pb.FundWholesaleAccountRequest) (*pb.FundWholesaleAccountResponse, error) {
	if !identity.ValidWholesaleAccountID(req.GetWholesaleAccountId()) {
		return nil, status.Error(codes.InvalidArgument, "wholesale_account_id is required")
	}
	if s.settlementDomainErr != nil || req.GetSettlementDomainId() == "" || req.GetSettlementDomainId() != s.settlementDomainID {
		return nil, status.Error(codes.PermissionDenied, "settlement domain does not match receiver ledger")
	}
	var pay pb.Payment
	if err := proto.Unmarshal(req.GetPaymentBytes(), &pay); err != nil || pay.GetTicketParams() == nil || len(pay.GetSender()) != 20 || len(pay.GetTicketSenderParams()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "funding payment is malformed")
	}
	digest := sha256.Sum256(req.GetPaymentBytes())
	fundingID := hex.EncodeToString(digest[:])
	response := func(r *store.FundingReceipt, replay bool) *pb.FundWholesaleAccountResponse {
		return &pb.FundWholesaleAccountResponse{FundingId: r.FundingID, Account: s.wholesaleAccountView(r.Account), CreditedValueWei: &pb.BigUInt{Value: decimalBytes(r.CreditedWei)}, Replayed: replay}
	}
	if receipt, err := s.store.GetFundingReceipt(pay.GetSender(), s.recipient, req.GetWholesaleAccountId(), fundingID); err == nil {
		return response(receipt, true), nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.PermissionDenied, "funding receipt identity mismatch")
	}
	workID := hex.EncodeToString(pay.GetTicketParams().GetRecipientRandHash())
	sess, err := s.store.Get(pay.GetSender(), workID)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, "funding generation not found")
	}
	if sess.WholesaleAccountID != req.GetWholesaleAccountId() || sess.TicketStreamID == "" {
		return nil, status.Error(codes.PermissionDenied, "funding generation account mismatch")
	}
	if err := checkSignedPrice(&pay, sess); err != nil {
		return nil, err
	}
	rand, ok := new(big.Int).SetString(sess.RecipientRand, 10)
	if !ok {
		return nil, status.Error(codes.FailedPrecondition, "invalid funding generation")
	}
	tp := pay.GetTicketParams()
	face := new(big.Int).SetBytes(tp.GetFaceValue())
	prob := new(big.Int).SetBytes(tp.GetWinProb())
	tickets := make([]store.FundingTicket, 0, len(pay.GetTicketSenderParams()))
	for _, param := range pay.GetTicketSenderParams() {
		t := &types.Ticket{Sender: pay.GetSender(), Recipient: tp.GetRecipient(), FaceValue: face, WinProb: prob, SenderNonce: param.GetSenderNonce(), RecipientRandHash: tp.GetRecipientRandHash(), CreationRound: pay.GetExpirationParams().GetCreationRound(), CreationRoundHash: pay.GetExpirationParams().GetCreationRoundBlockHash()}
		if err := validator.Validate(s.recipient, t, param.GetSig(), rand); err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "invalid funding ticket: %v", err)
		}
		item := store.FundingTicket{Nonce: t.SenderNonce, Credit: types.CreditedEV(face, prob), Hash: t.Hash()}
		if validator.IsWinning(t, param.GetSig(), rand) {
			item.Winner = &store.SignedTicket{Sender: t.Sender, Recipient: t.Recipient, FaceValue: face, WinProb: prob, SenderNonce: t.SenderNonce, RecipientRandHash: t.RecipientRandHash, CreationRound: t.CreationRound, CreationRoundHash: t.CreationRoundHash, Sig: param.GetSig(), RecipientRand: rand}
		}
		tickets = append(tickets, item)
	}
	receipt, replay, err := s.store.ApplyWholesaleFunding(pay.GetSender(), s.recipient, req.GetWholesaleAccountId(), workID, fundingID, tickets, time.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrClosed) || errors.Is(err, store.ErrTooManyNonces) {
			st, _ := status.New(codes.FailedPrecondition, "funding generation retired or exhausted").WithDetails(&errdetails.ErrorInfo{Reason: "INVALID_RECIPIENT_RAND", Domain: "payments.livepeer.org"})
			return nil, st.Err()
		}
		return nil, status.Errorf(codes.FailedPrecondition, "fund wholesale account: %v", err)
	}
	if !replay {
		statuses := make([]*pb.TicketStatus, 0, len(tickets))
		var winners int32
		for _, ticket := range tickets {
			st := &pb.TicketStatus{SenderNonce: ticket.Nonce, CreditedEv: ticket.Credit.Bytes(), WasWinning: ticket.Winner != nil}
			if st.WasWinning {
				winners++
			}
			statuses = append(statuses, st)
		}
		s.recordPaymentMetrics(statuses, winners, new(big.Int).SetBytes(decimalBytes(receipt.CreditedWei)))
	}
	s.recordWholesaleTotals()
	return response(receipt, replay), nil
}
