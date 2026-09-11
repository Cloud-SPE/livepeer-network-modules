package payment

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// GRPC is the broker's real PayeeDaemon adapter, talking over a unix
// socket. NewGRPC dials eagerly + Health-probes; gRPC handles
// reconnection internally for the daemon's lifetime.
type GRPC struct {
	conn   *grpc.ClientConn
	client pb.PayeeDaemonClient
	socket string
}

// NewGRPC dials the unix socket and Health-probes the daemon. Fails
// fast if the daemon is unreachable.
func NewGRPC(ctx context.Context, socketPath string) (*GRPC, error) {
	conn, err := grpc.NewClient(
		"unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial unix socket %s: %w", socketPath, err)
	}
	g := &GRPC{
		conn:   conn,
		client: pb.NewPayeeDaemonClient(conn),
		socket: socketPath,
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := g.client.Health(probeCtx, &pb.HealthRequest{}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("payment-daemon health probe at %s: %w", socketPath, err)
	}
	return g, nil
}

// Shutdown closes the underlying gRPC connection. Called once at broker
// shutdown.
func (g *GRPC) Shutdown() error {
	if g.conn == nil {
		return nil
	}
	return g.conn.Close()
}

func (g *GRPC) GetTicketParams(ctx context.Context, req GetTicketParamsRequest) (*TicketParams, error) {
	faceValue := []byte(nil)
	if req.FaceValue != nil {
		faceValue = req.FaceValue.Bytes()
	}
	resp, err := g.client.GetTicketParams(ctx, &pb.GetTicketParamsRequest{
		Sender:     req.Sender,
		Recipient:  req.Recipient,
		FaceValue:  faceValue,
		Capability: req.Capability,
		Offering:   req.Offering,
	})
	if err != nil {
		return nil, err
	}
	tp := resp.GetTicketParams()
	if tp == nil {
		return &TicketParams{}, nil
	}
	out := &TicketParams{
		Recipient:         append([]byte(nil), tp.GetRecipient()...),
		FaceValue:         new(big.Int).SetBytes(tp.GetFaceValue()),
		WinProb:           new(big.Int).SetBytes(tp.GetWinProb()),
		RecipientRandHash: append([]byte(nil), tp.GetRecipientRandHash()...),
		Seed:              append([]byte(nil), tp.GetSeed()...),
		ExpirationBlock:   new(big.Int).SetBytes(tp.GetExpirationBlock()),
	}
	out.HighestSeenNonce = resp.GetHighestSeenNonce()
	out.HasSeenNonces = resp.GetHasSeenNonces()
	if exp := tp.GetExpirationParams(); exp != nil {
		out.ExpirationParams = &TicketExpirationParams{
			CreationRound:          exp.GetCreationRound(),
			CreationRoundBlockHash: append([]byte(nil), exp.GetCreationRoundBlockHash()...),
		}
	}
	return out, nil
}

func (g *GRPC) OpenSession(ctx context.Context, req OpenSessionRequest) (*OpenSessionResult, error) {
	priceBytes := []byte(nil)
	if req.PricePerWorkUnitWei != nil {
		priceBytes = req.PricePerWorkUnitWei.Bytes()
	}
	resp, err := g.client.OpenSession(ctx, &pb.OpenSessionRequest{
		WorkId:              req.WorkID,
		Capability:          req.Capability,
		Offering:            req.Offering,
		PricePerWorkUnitWei: priceBytes,
		PerUnits:            req.PerUnits,
		WorkUnit:            req.WorkUnit,
	})
	if err != nil {
		return nil, err
	}
	return &OpenSessionResult{
		AlreadyOpen: resp.GetOutcome() == pb.OpenSessionResponse_OUTCOME_ALREADY_OPEN,
	}, nil
}

func (g *GRPC) ProcessPayment(ctx context.Context, req ProcessPaymentRequest) (*ProcessPaymentResult, error) {
	resp, err := g.client.ProcessPayment(ctx, &pb.ProcessPaymentRequest{
		PaymentBytes: req.PaymentBytes,
		WorkId:       req.WorkID,
	})
	if err != nil {
		return nil, err
	}
	ticketStatus := make([]TicketStatus, 0, len(resp.GetTicketStatus()))
	for _, st := range resp.GetTicketStatus() {
		ticketStatus = append(ticketStatus, TicketStatus{
			SenderNonce:     st.GetSenderNonce(),
			RejectionReason: PaymentRejectionReason(st.GetRejectionReason()),
			CreditedEV:      new(big.Int).SetBytes(st.GetCreditedEv()),
			WasWinning:      st.GetWasWinning(),
		})
	}
	return &ProcessPaymentResult{
		Sender:            resp.GetSender(),
		CreditedEV:        new(big.Int).SetBytes(resp.GetCreditedEv()),
		Balance:           new(big.Int).SetBytes(resp.GetBalance()),
		WinnersQueued:     resp.GetWinnersQueued(),
		TicketStatus:      ticketStatus,
		TicketsRejected:   resp.GetTicketsRejected(),
		DominantRejection: PaymentRejectionReason(resp.GetDominantRejection()),
	}, nil
}

func accountFromProto(in *pb.WholesaleAccountView) *WholesaleAccount {
	if in == nil {
		return nil
	}
	return &WholesaleAccount{
		Payer: append([]byte(nil), in.GetPayer()...), Payee: append([]byte(nil), in.GetPayee()...),
		Credited:  new(big.Int).SetBytes(in.GetCreditedValueWei().GetValue()),
		Reserved:  new(big.Int).SetBytes(in.GetReservedValueWei().GetValue()),
		Debited:   new(big.Int).SetBytes(in.GetDebitedValueWei().GetValue()),
		Available: new(big.Int).SetBytes(in.GetAvailableValueWei().GetValue()),
		Version:   in.GetVersion(), ObservedAt: in.GetObservedAt(), ChainID: in.GetChainId(), Denomination: in.GetDenomination(),
	}
}

func (g *GRPC) FundWholesaleAccount(ctx context.Context, paymentBytes []byte) (*FundWholesaleAccountResult, error) {
	res, err := g.client.FundWholesaleAccount(ctx, &pb.FundWholesaleAccountRequest{PaymentBytes: paymentBytes})
	if err != nil {
		return nil, err
	}
	return &FundWholesaleAccountResult{Account: accountFromProto(res.GetAccount()), Credited: new(big.Int).SetBytes(res.GetCreditedValueWei().GetValue()), Replayed: res.GetReplayed()}, nil
}

func (g *GRPC) AdmitAuthorization(ctx context.Context, req AdmitAuthorizationRequest) (*AdmitAuthorizationResult, error) {
	reservation := []byte(nil)
	if req.Reservation != nil {
		reservation = req.Reservation.Bytes()
	}
	res, err := g.client.AdmitAuthorization(ctx, &pb.AdmitAuthorizationRequest{AuthorizationBytes: req.AuthorizationBytes, PaymentBytes: req.PaymentBytes, ReservationValueWei: &pb.BigUInt{Value: reservation}})
	if err != nil {
		return nil, err
	}
	return &AdmitAuthorizationResult{State: int32(res.GetState()), Account: accountFromProto(res.GetAccount()), Reserved: new(big.Int).SetBytes(res.GetReservedValueWei().GetValue()), Credited: new(big.Int).SetBytes(res.GetCreditedValueWei().GetValue()), Replayed: res.GetReplayed()}, nil
}

func (g *GRPC) AdvanceAuthorization(ctx context.Context, req AdvanceAuthorizationRequest) (*AdvanceAuthorizationResult, error) {
	target := []byte(nil)
	if req.TargetReserved != nil {
		target = req.TargetReserved.Bytes()
	}
	res, err := g.client.AdvanceAuthorization(ctx, &pb.AdvanceAuthorizationRequest{Payer: req.Payer, AuthorizationId: req.AuthorizationID, CumulativeUnits: req.CumulativeUnits, TargetReservedValueWei: &pb.BigUInt{Value: target}, AdvanceSeq: req.AdvanceSeq, PaymentBytes: req.PaymentBytes})
	if err != nil {
		return nil, err
	}
	return &AdvanceAuthorizationResult{State: int32(res.GetState()), Account: accountFromProto(res.GetAccount()), BilledDelta: new(big.Int).SetBytes(res.GetBilledDeltaWei().GetValue()), CumulativeBilled: new(big.Int).SetBytes(res.GetCumulativeBilledValueWei().GetValue()), Reserved: new(big.Int).SetBytes(res.GetReservedValueWei().GetValue()), Credited: new(big.Int).SetBytes(res.GetCreditedValueWei().GetValue()), Replayed: res.GetReplayed()}, nil
}

func (g *GRPC) SettleAuthorization(ctx context.Context, req SettleAuthorizationRequest) (*SettleAuthorizationResult, error) {
	res, err := g.client.SettleAuthorization(ctx, &pb.SettleAuthorizationRequest{Payer: req.Payer, AuthorizationId: req.AuthorizationID, ActualUnits: req.ActualUnits, SettlementSeq: req.SettlementSeq})
	if err != nil {
		return nil, err
	}
	return &SettleAuthorizationResult{State: int32(res.GetState()), Account: accountFromProto(res.GetAccount()), Billed: new(big.Int).SetBytes(res.GetBilledValueWei().GetValue()), Released: new(big.Int).SetBytes(res.GetReleasedValueWei().GetValue()), Replayed: res.GetReplayed()}, nil
}

func (g *GRPC) GetWholesaleAccount(ctx context.Context, payer []byte) (*WholesaleAccount, error) {
	res, err := g.client.GetWholesaleAccount(ctx, &pb.GetWholesaleAccountRequest{Payer: payer})
	if err != nil {
		return nil, err
	}
	return accountFromProto(res.GetAccount()), nil
}

func (g *GRPC) GetSpendAuthorization(ctx context.Context, payer []byte, authorizationID string) (*SpendAuthorizationStatus, error) {
	res, err := g.client.GetSpendAuthorization(ctx, &pb.GetSpendAuthorizationRequest{Payer: payer, AuthorizationId: authorizationID})
	if err != nil {
		return nil, err
	}
	return &SpendAuthorizationStatus{State: int32(res.GetState()), Reserved: new(big.Int).SetBytes(res.GetReservedValueWei().GetValue()), Billed: new(big.Int).SetBytes(res.GetBilledValueWei().GetValue()), Released: new(big.Int).SetBytes(res.GetReleasedValueWei().GetValue()), ActualUnits: res.GetActualUnits(), SettlementSeq: res.GetSettlementSeq(), ObservedAt: res.GetObservedAt()}, nil
}

func (g *GRPC) DebitBalance(ctx context.Context, req DebitBalanceRequest) (*DebitResult, error) {
	resp, err := g.client.DebitBalance(ctx, &pb.DebitBalanceRequest{
		Sender:    req.Sender,
		WorkId:    req.WorkID,
		WorkUnits: req.WorkUnits,
		DebitSeq:  req.DebitSeq,
	})
	if err != nil {
		return nil, err
	}
	return &DebitResult{
		Balance:         new(big.Int).SetBytes(resp.GetBalance()),
		DebitedWei:      new(big.Int).SetBytes(resp.GetDebitedWei().GetValue()),
		CumulativeUnits: resp.GetCumulativeUnits(),
		Replayed:        resp.GetReplayed(),
	}, nil
}

func (g *GRPC) SufficientBalance(ctx context.Context, req SufficientBalanceRequest) (*SufficientBalanceResult, error) {
	resp, err := g.client.SufficientBalance(ctx, &pb.SufficientBalanceRequest{
		Sender:       req.Sender,
		WorkId:       req.WorkID,
		MinWorkUnits: req.MinWorkUnits,
	})
	if err != nil {
		return nil, err
	}
	return &SufficientBalanceResult{
		Sufficient: resp.GetSufficient(),
		Balance:    new(big.Int).SetBytes(resp.GetBalance()),
	}, nil
}

func (g *GRPC) GetBalance(ctx context.Context, sender []byte, workID string) (*big.Int, error) {
	resp, err := g.client.GetBalance(ctx, &pb.GetBalanceRequest{
		Sender: sender,
		WorkId: workID,
	})
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(resp.GetBalance()), nil
}

func (g *GRPC) CloseSession(ctx context.Context, sender []byte, workID string) error {
	_, err := g.client.CloseSession(ctx, &pb.CloseSessionRequest{
		Sender: sender,
		WorkId: workID,
	})
	return err
}

// Compile-time interface check.
var _ Client = (*GRPC)(nil)
var _ AccountClient = (*GRPC)(nil)
