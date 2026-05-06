// Package types holds pure data shared across sender, receiver, escrow,
// and settlement. No providers, no I/O — just structs, constants, and
// hash helpers.
//
// The wire-format messages live in
// `livepeer-network-protocol/proto-go/livepeer/payments/v1`; the structs
// here are richer in-process forms (e.g. `*big.Int` instead of
// big-endian bytes) used by the service code.
package types

import (
	"errors"
	"fmt"
	"math/big"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// MaxWinProb is `2^256 - 1`. Win probabilities are encoded as 256-bit
// big-endian integers; `WinProb / MaxWinProb` is the actual probability
// in [0, 1].
var MaxWinProb = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

// ErrTicketParamsExpired is returned when sender or receiver code looks
// at a TicketParams whose expiration_block has passed.
var ErrTicketParamsExpired = errors.New("ticket params expired")

// TicketParams is the in-process form of `pb.TicketParams`. Sender and
// receiver convert to/from the wire form at the boundary; the rest of
// the code path uses this form so big-int math is clean.
type TicketParams struct {
	Recipient         []byte // 20 bytes
	FaceValue         *big.Int
	WinProb           *big.Int
	RecipientRandHash []byte // 32 bytes
	Seed              []byte
	ExpirationBlock   *big.Int
	ExpirationParams  *TicketExpirationParams
}

// TicketExpirationParams pins the protocol round + block hash a ticket
// was created in.
type TicketExpirationParams struct {
	CreationRound          int64
	CreationRoundBlockHash []byte
}

// Ticket is a single signable claim. The sender constructs one Ticket
// per CreateTicketBatch entry; the hash is what gets signed.
type Ticket struct {
	Recipient         []byte
	Sender            []byte
	FaceValue         *big.Int
	WinProb           *big.Int
	SenderNonce       uint32
	RecipientRandHash []byte
	CreationRound     int64
	CreationRoundHash []byte
}

// TicketBatch is the result of CreateTicketBatch — a fully-formed
// Payment with one or more signed tickets.
type TicketBatch struct {
	TicketParams        *TicketParams
	Sender              []byte
	ExpirationParams    *TicketExpirationParams
	TicketSenderParams  []*TicketSenderParams
	ExpectedPrice       *PriceInfo
}

// TicketSenderParams is the in-process form of `pb.TicketSenderParams`.
type TicketSenderParams struct {
	SenderNonce uint32
	Sig         []byte // 65 bytes [R||S||V]
}

// PriceInfo is the in-process form of `pb.PriceInfo`. The `Capability`
// uint32 is the wire-level capability ID; daemon code translates string
// capability names to/from this at the RPC boundary.
type PriceInfo struct {
	PricePerUnit  int64
	PixelsPerUnit int64
	Capability    uint32
	Constraint    string
}

// EV returns the per-ticket expected value in wei: `face_value × win_prob /
// 2^256`. Returns nil if either input is nil.
func EV(faceValue, winProb *big.Int) *big.Rat {
	if faceValue == nil || winProb == nil {
		return nil
	}
	num := new(big.Rat).SetInt(new(big.Int).Mul(faceValue, winProb))
	den := new(big.Rat).SetInt(MaxWinProb)
	return new(big.Rat).Quo(num, den)
}

// ToWirePayment converts an in-process TicketBatch into a wire
// `pb.Payment` ready for serialization.
func (b *TicketBatch) ToWirePayment() *pb.Payment {
	if b == nil {
		return nil
	}
	out := &pb.Payment{
		Sender:           b.Sender,
		ExpirationParams: expirationParamsToWire(b.ExpirationParams),
		TicketParams:     ticketParamsToWire(b.TicketParams),
		ExpectedPrice:    priceInfoToWire(b.ExpectedPrice),
	}
	for _, sp := range b.TicketSenderParams {
		out.TicketSenderParams = append(out.TicketSenderParams, &pb.TicketSenderParams{
			SenderNonce: sp.SenderNonce,
			Sig:         sp.Sig,
		})
	}
	return out
}

// TicketParamsFromWire converts a wire `pb.TicketParams` into the
// in-process form. Big-endian bytes become `*big.Int`.
func TicketParamsFromWire(in *pb.TicketParams) *TicketParams {
	if in == nil {
		return nil
	}
	out := &TicketParams{
		Recipient:         in.GetRecipient(),
		FaceValue:         new(big.Int).SetBytes(in.GetFaceValue()),
		WinProb:           new(big.Int).SetBytes(in.GetWinProb()),
		RecipientRandHash: in.GetRecipientRandHash(),
		Seed:              in.GetSeed(),
		ExpirationBlock:   new(big.Int).SetBytes(in.GetExpirationBlock()),
	}
	if e := in.GetExpirationParams(); e != nil {
		out.ExpirationParams = &TicketExpirationParams{
			CreationRound:          e.GetCreationRound(),
			CreationRoundBlockHash: e.GetCreationRoundBlockHash(),
		}
	}
	return out
}

// ParseFaceValue parses big-endian wei bytes into a *big.Int. Returns an
// error if the input is empty or longer than 32 bytes (uint256 cap).
func ParseFaceValue(raw []byte) (*big.Int, error) {
	if len(raw) == 0 {
		return nil, errors.New("face_value is empty")
	}
	if len(raw) > 32 {
		return nil, fmt.Errorf("face_value is %d bytes; uint256 cap is 32", len(raw))
	}
	return new(big.Int).SetBytes(raw), nil
}

func expirationParamsToWire(e *TicketExpirationParams) *pb.TicketExpirationParams {
	if e == nil {
		return nil
	}
	return &pb.TicketExpirationParams{
		CreationRound:          e.CreationRound,
		CreationRoundBlockHash: e.CreationRoundBlockHash,
	}
}

func ticketParamsToWire(p *TicketParams) *pb.TicketParams {
	if p == nil {
		return nil
	}
	out := &pb.TicketParams{
		Recipient:         p.Recipient,
		FaceValue:         bigIntBytes(p.FaceValue),
		WinProb:           bigIntBytes(p.WinProb),
		RecipientRandHash: p.RecipientRandHash,
		Seed:              p.Seed,
		ExpirationBlock:   bigIntBytes(p.ExpirationBlock),
		ExpirationParams:  expirationParamsToWire(p.ExpirationParams),
	}
	return out
}

func priceInfoToWire(p *PriceInfo) *pb.PriceInfo {
	if p == nil {
		return nil
	}
	return &pb.PriceInfo{
		PricePerUnit:  p.PricePerUnit,
		PixelsPerUnit: p.PixelsPerUnit,
		Capability:    p.Capability,
		Constraint:    p.Constraint,
	}
}

func bigIntBytes(n *big.Int) []byte {
	if n == nil {
		return nil
	}
	return n.Bytes()
}
