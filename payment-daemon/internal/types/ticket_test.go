package types

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"

	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestTicketHash_GoLivepeerLayout pins the keccak256-flatten output for a
// hand-built ticket. Compares against an independently-computed hash to
// catch any drift in the layout (field order, padding, aux-data
// inclusion).
func TestTicketHash_GoLivepeerLayout(t *testing.T) {
	recipient, _ := hex.DecodeString("01020304050607080910111213141516171819ff")
	sender, _ := hex.DecodeString("a0a1a2a3a4a5a6a7a8a9aaabacadaeaf b0b1b2b3")
	sender, _ = hex.DecodeString("a0a1a2a3a4a5a6a7a8a9aaabacadaeafb0b1b2b3")
	faceValue := big.NewInt(1_000_000_000)
	winProb := new(big.Int).SetUint64(0xff_ff_ff_ff_ff_ff_ff_ff)
	rand := big.NewInt(42)
	rrHash := crypto.Keccak256(ethcommon.LeftPadBytes(rand.Bytes(), 32))
	creationRound := int64(12345)
	creationRoundHash := bytes.Repeat([]byte{0x70}, 32)

	tk := &Ticket{
		Recipient:         recipient,
		Sender:            sender,
		FaceValue:         faceValue,
		WinProb:           winProb,
		SenderNonce:       7,
		RecipientRandHash: rrHash,
		CreationRound:     creationRound,
		CreationRoundHash: creationRoundHash,
	}

	want := computeExpectedHash(t, tk)
	got := tk.Hash()

	if !bytes.Equal(got, want) {
		t.Errorf("ticket hash mismatch:\n got=%x\n want=%x", got, want)
	}
}

// TestTicketHash_NoAuxData checks the empty-auxData branch — when both
// CreationRound and the block hash are zero, AuxData() must return [] and
// the flatten must end at recipientRandHash.
func TestTicketHash_NoAuxData(t *testing.T) {
	tk := &Ticket{
		Recipient:         bytes.Repeat([]byte{0x01}, 20),
		Sender:            bytes.Repeat([]byte{0x02}, 20),
		FaceValue:         big.NewInt(1),
		WinProb:           big.NewInt(2),
		SenderNonce:       3,
		RecipientRandHash: bytes.Repeat([]byte{0x04}, 32),
		CreationRound:     0,
		CreationRoundHash: make([]byte, 32),
	}
	if got := tk.AuxData(); len(got) != 0 {
		t.Errorf("AuxData with zero round + zero hash should be empty; got %d bytes", len(got))
	}
}

// TestHashRecipientRand_Symmetric checks that HashRecipientRand of a
// known preimage matches keccak256(LeftPad(rand, 32)).
func TestHashRecipientRand_Symmetric(t *testing.T) {
	rand := big.NewInt(99)
	got := HashRecipientRand(rand)
	want := crypto.Keccak256(ethcommon.LeftPadBytes(rand.Bytes(), 32))
	if !bytes.Equal(got, want) {
		t.Errorf("HashRecipientRand mismatch: got=%x want=%x", got, want)
	}
}

// TestWinningHash_LessThanWinProb verifies the winning predicate works
// against a contrived win-prob max value.
func TestWinningHash_LessThan(t *testing.T) {
	sig := bytes.Repeat([]byte{0xaa}, 65)
	rand := big.NewInt(1)
	wh := WinningHash(sig, rand)
	if wh.Sign() <= 0 {
		t.Fatalf("WinningHash should be > 0 for non-zero inputs; got %s", wh.String())
	}
	// MaxWinProb (2^256-1) — every ticket wins.
	if wh.Cmp(MaxWinProb) >= 0 {
		t.Errorf("WinningHash should always be < 2^256; got %s", wh.String())
	}
}

// computeExpectedHash recomputes the ticket hash using the contract
// flatten layout independently of the Ticket.Hash code path so the test
// catches drift in either direction.
func computeExpectedHash(t *testing.T, tk *Ticket) []byte {
	t.Helper()
	var buf []byte
	buf = append(buf, ethcommon.LeftPadBytes(tk.Recipient, 20)...)
	buf = append(buf, ethcommon.LeftPadBytes(tk.Sender, 20)...)
	buf = append(buf, ethcommon.LeftPadBytes(tk.FaceValue.Bytes(), 32)...)
	buf = append(buf, ethcommon.LeftPadBytes(tk.WinProb.Bytes(), 32)...)
	buf = append(buf, ethcommon.LeftPadBytes(new(big.Int).SetUint64(uint64(tk.SenderNonce)).Bytes(), 32)...)
	buf = append(buf, ethcommon.LeftPadBytes(tk.RecipientRandHash, 32)...)
	if tk.CreationRound != 0 || !allZero(tk.CreationRoundHash) {
		buf = append(buf, ethcommon.LeftPadBytes(big.NewInt(tk.CreationRound).Bytes(), 32)...)
		buf = append(buf, ethcommon.LeftPadBytes(tk.CreationRoundHash, 32)...)
	}
	return crypto.Keccak256(buf)
}
