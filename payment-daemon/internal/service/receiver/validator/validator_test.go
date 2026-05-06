package validator_test

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/keystore/inmemory"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/receiver/validator"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/types"
)

func makeKey(t *testing.T) *inmemory.KeyStore {
	t.Helper()
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	ks, err := inmemory.New(priv)
	if err != nil {
		t.Fatalf("inmemory.New: %v", err)
	}
	return ks
}

func TestValidate_RoundTrip(t *testing.T) {
	ks := makeKey(t)
	rand := big.NewInt(99)

	recipient := bytes.Repeat([]byte{0x01}, 20)
	tk := &types.Ticket{
		Recipient:         recipient,
		Sender:            ks.Address(),
		FaceValue:         big.NewInt(1_000_000),
		WinProb:           types.MaxWinProb,
		SenderNonce:       1,
		RecipientRandHash: types.HashRecipientRand(rand),
		CreationRound:     12345,
		CreationRoundHash: bytes.Repeat([]byte{0x70}, 32),
	}
	sig, err := ks.Sign(tk.Hash())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if err := validator.Validate(recipient, tk, sig, rand); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestValidate_RejectsRecipientMismatch(t *testing.T) {
	ks := makeKey(t)
	rand := big.NewInt(1)
	tk := &types.Ticket{
		Recipient:         bytes.Repeat([]byte{0x01}, 20),
		Sender:            ks.Address(),
		FaceValue:         big.NewInt(1),
		WinProb:           big.NewInt(1),
		SenderNonce:       1,
		RecipientRandHash: types.HashRecipientRand(rand),
	}
	sig, _ := ks.Sign(tk.Hash())
	wrong := bytes.Repeat([]byte{0xff}, 20)
	if err := validator.Validate(wrong, tk, sig, rand); err != validator.ErrInvalidRecipient {
		t.Errorf("err = %v; want ErrInvalidRecipient", err)
	}
}

func TestValidate_RejectsRandPreimageMismatch(t *testing.T) {
	ks := makeKey(t)
	tk := &types.Ticket{
		Recipient:         bytes.Repeat([]byte{0x01}, 20),
		Sender:            ks.Address(),
		FaceValue:         big.NewInt(1),
		WinProb:           big.NewInt(1),
		SenderNonce:       1,
		RecipientRandHash: types.HashRecipientRand(big.NewInt(1)),
	}
	sig, _ := ks.Sign(tk.Hash())
	if err := validator.Validate(tk.Recipient, tk, sig, big.NewInt(99)); err != validator.ErrInvalidRecipientRand {
		t.Errorf("err = %v; want ErrInvalidRecipientRand", err)
	}
}

func TestValidate_RejectsBadSignature(t *testing.T) {
	ks := makeKey(t)
	other := makeKey(t)
	rand := big.NewInt(7)
	tk := &types.Ticket{
		Recipient:         bytes.Repeat([]byte{0x01}, 20),
		Sender:            ks.Address(),
		FaceValue:         big.NewInt(1),
		WinProb:           big.NewInt(1),
		SenderNonce:       1,
		RecipientRandHash: types.HashRecipientRand(rand),
	}
	// Sign with a different key — recovery yields a different address.
	sig, _ := other.Sign(tk.Hash())
	if err := validator.Validate(tk.Recipient, tk, sig, rand); err != validator.ErrInvalidSignature {
		t.Errorf("err = %v; want ErrInvalidSignature", err)
	}
}

func TestIsWinning_HighWinProbWins(t *testing.T) {
	ks := makeKey(t)
	rand := big.NewInt(11)
	tk := &types.Ticket{
		Recipient:         bytes.Repeat([]byte{0x01}, 20),
		Sender:            ks.Address(),
		FaceValue:         big.NewInt(1),
		WinProb:           types.MaxWinProb,
		SenderNonce:       1,
		RecipientRandHash: types.HashRecipientRand(rand),
	}
	sig, _ := ks.Sign(tk.Hash())
	if !validator.IsWinning(tk, sig, rand) {
		t.Error("MaxWinProb ticket should always win")
	}
}

func TestIsWinning_ZeroWinProbNeverWins(t *testing.T) {
	ks := makeKey(t)
	rand := big.NewInt(11)
	tk := &types.Ticket{
		Recipient:         bytes.Repeat([]byte{0x01}, 20),
		Sender:            ks.Address(),
		FaceValue:         big.NewInt(1),
		WinProb:           big.NewInt(0),
		SenderNonce:       1,
		RecipientRandHash: types.HashRecipientRand(rand),
	}
	sig, _ := ks.Sign(tk.Hash())
	if validator.IsWinning(tk, sig, rand) {
		t.Error("zero-WinProb ticket should never win")
	}
}
