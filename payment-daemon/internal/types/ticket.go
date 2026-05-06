package types

import (
	"math/big"

	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Solidity-sized constants for the ticket flatten layout. Pinned by the
// on-chain TicketBroker contract; do not edit.
const (
	addressSize = 20
	uint256Size = 32
	bytes32Size = 32
)

// SignedTicket pairs a Ticket with the sender's signature and the
// recipient-rand preimage. This is the form used by validation and
// redemption.
type SignedTicket struct {
	*Ticket
	Sig           []byte
	RecipientRand *big.Int
}

// Hash returns keccak256 over the contract-defined flatten layout:
//
//	recipient(20) || sender(20) || faceValue(u256) || winProb(u256) ||
//	senderNonce(u256) || recipientRandHash(32) || auxData(0|64)
//
// auxData is empty when CreationRound == 0 and the block hash is all-zero.
func (t *Ticket) Hash() []byte {
	return crypto.Keccak256(t.flatten())
}

// AuxData encodes (CreationRound, CreationRoundHash) per
// MixinTicketProcessor.sol. Returns an empty slice when both fields are
// zero — matches go-livepeer's behaviour.
func (t *Ticket) AuxData() []byte {
	zero := allZero(t.CreationRoundHash)
	if t.CreationRound == 0 && zero {
		return []byte{}
	}
	out := make([]byte, 0, uint256Size+bytes32Size)
	out = append(out, ethcommon.LeftPadBytes(big.NewInt(t.CreationRound).Bytes(), uint256Size)...)
	hash := t.CreationRoundHash
	if len(hash) > bytes32Size {
		hash = hash[:bytes32Size]
	}
	out = append(out, ethcommon.LeftPadBytes(hash, bytes32Size)...)
	return out
}

// HashRecipientRand returns keccak256(LeftPadBytes(rand, 32)). The receiver
// embeds this in TicketParams; redemption reveals `rand` as the preimage.
func HashRecipientRand(rand *big.Int) []byte {
	if rand == nil {
		rand = new(big.Int)
	}
	return crypto.Keccak256(ethcommon.LeftPadBytes(rand.Bytes(), uint256Size))
}

// WinningHash returns keccak256(sig || LeftPadBytes(rand, 32)) as a
// non-negative big.Int. A ticket wins iff this value is strictly less
// than ticket.WinProb.
func WinningHash(sig []byte, rand *big.Int) *big.Int {
	if rand == nil {
		rand = new(big.Int)
	}
	randBytes := ethcommon.LeftPadBytes(rand.Bytes(), uint256Size)
	return new(big.Int).SetBytes(crypto.Keccak256(sig, randBytes))
}

func (t *Ticket) flatten() []byte {
	aux := t.AuxData()
	buf := make([]byte, 0, addressSize+addressSize+uint256Size+uint256Size+uint256Size+bytes32Size+len(aux))
	buf = append(buf, leftPad(t.Recipient, addressSize)...)
	buf = append(buf, leftPad(t.Sender, addressSize)...)
	buf = append(buf, ethcommon.LeftPadBytes(safeBytes(t.FaceValue), uint256Size)...)
	buf = append(buf, ethcommon.LeftPadBytes(safeBytes(t.WinProb), uint256Size)...)
	buf = append(buf, ethcommon.LeftPadBytes(new(big.Int).SetUint64(uint64(t.SenderNonce)).Bytes(), uint256Size)...)
	buf = append(buf, leftPad(t.RecipientRandHash, bytes32Size)...)
	buf = append(buf, aux...)
	return buf
}

func safeBytes(v *big.Int) []byte {
	if v == nil {
		return nil
	}
	return v.Bytes()
}

// leftPad returns a copy of `b` left-padded with zero bytes to length n.
// Truncates from the front if `b` is already longer.
func leftPad(b []byte, n int) []byte {
	if len(b) >= n {
		out := make([]byte, n)
		copy(out, b[len(b)-n:])
		return out
	}
	out := make([]byte, n)
	copy(out[n-len(b):], b)
	return out
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
