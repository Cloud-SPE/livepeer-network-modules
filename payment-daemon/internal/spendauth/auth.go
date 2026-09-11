// Package spendauth implements the payer-signed, single-purpose wholesale
// account authorization shared by sender and receiver services.
package spendauth

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

const Domain = "livepeer-spend-authorization/v1"

var (
	ErrMalformedSignature = errors.New("spend authorization signature is malformed")
	ErrInvalidSignature   = errors.New("spend authorization signature does not match payer")
)

// Digest returns the domain payload digest supplied to the keystore's
// EIP-191 Sign method. Deterministic protobuf is part of the wire contract.
func Digest(payload *pb.SpendAuthorizationPayload) ([]byte, error) {
	if payload == nil {
		return nil, errors.New("spend authorization payload is nil")
	}
	if payload.GetDomain() != Domain {
		return nil, fmt.Errorf("spend authorization domain %q is unsupported", payload.GetDomain())
	}
	b, err := (proto.MarshalOptions{Deterministic: true}).Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal spend authorization payload: %w", err)
	}
	return crypto.Keccak256(b), nil
}

// Verify recovers the EIP-191 signer and requires it to equal payload.payer.
func Verify(auth *pb.SpendAuthorization) error {
	if auth == nil || auth.GetPayload() == nil {
		return errors.New("spend authorization payload is required")
	}
	payer := auth.GetPayload().GetPayer()
	if len(payer) != 20 {
		return errors.New("spend authorization payer must be 20 bytes")
	}
	sig := auth.GetSignature()
	if len(sig) != 65 || (sig[64] != 27 && sig[64] != 28) {
		return ErrMalformedSignature
	}
	digest, err := Digest(auth.GetPayload())
	if err != nil {
		return err
	}
	normalized := append([]byte(nil), sig...)
	normalized[64] -= 27
	pub, err := crypto.SigToPub(accounts.TextHash(digest), normalized)
	if err != nil {
		return ErrInvalidSignature
	}
	if !bytes.Equal(crypto.PubkeyToAddress(*pub).Bytes(), payer) {
		return ErrInvalidSignature
	}
	return nil
}
