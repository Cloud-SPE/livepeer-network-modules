// Package chain holds the typed domain values used across chain-commons.
//
// All types here are pure data with no I/O dependencies. Aliases over
// go-ethereum's common.* types let consumers avoid importing go-ethereum
// just to name a hash or address.
package chain

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// Address is a 20-byte Ethereum account or contract address.
type Address = common.Address

// TxHash is a 32-byte transaction or block hash.
type TxHash = common.Hash

// BlockNumber is an L1 or L2 block height. Always non-negative.
type BlockNumber uint64

// String returns the decimal string form for logs and CLI output.
func (b BlockNumber) String() string { return fmt.Sprintf("%d", b) }

// BigInt returns the value as a *big.Int (heap-allocated). Useful for
// passing to go-ethereum APIs that take *big.Int.
func (b BlockNumber) BigInt() *big.Int { return new(big.Int).SetUint64(uint64(b)) }

// Bytes returns the value as 8 bytes big-endian. Used as a BoltDB key.
func (b BlockNumber) Bytes() []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, uint64(b))
	return out
}

// RoundNumber is a Livepeer protocol round number. Always non-negative.
type RoundNumber uint64

// String returns the decimal string form.
func (r RoundNumber) String() string { return fmt.Sprintf("%d", r) }

// BigInt returns the value as a *big.Int.
func (r RoundNumber) BigInt() *big.Int { return new(big.Int).SetUint64(uint64(r)) }

// Bytes returns the value as 8 bytes big-endian.
func (r RoundNumber) Bytes() []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, uint64(r))
	return out
}

// ChainID identifies which Ethereum-compatible chain the daemon talks to.
// Arbitrum One is 42161; Arbitrum Sepolia is 421614; Anvil is operator-set.
type ChainID uint64

// BigInt returns the value as a *big.Int.
func (c ChainID) BigInt() *big.Int { return new(big.Int).SetUint64(uint64(c)) }

// Wei is an amount in the smallest Ethereum currency unit. Always a pointer
// so the zero value is a sentinel rather than 0; callers must construct with
// big.NewInt or new(big.Int).
type Wei = *big.Int

// GasPrice is an alias for Wei used in gas-pricing contexts. Same underlying
// type; the alias documents intent.
type GasPrice = *big.Int

// Round is the typed event struct emitted by services/roundclock and consumed
// by daemons that react to round transitions.
type Round struct {
	// Number is the protocol round number (currently active when this Round
	// event fires).
	Number RoundNumber

	// StartBlock is the L2 block at which this round became current.
	StartBlock BlockNumber

	// L1StartBlock is the Arbitrum-recorded L1 block at which this round
	// became current. Sourced from the L2 block header's l1BlockNumber field.
	L1StartBlock BlockNumber

	// Length is the round length in blocks.
	Length BlockNumber

	// Initialized reports whether RoundsManager.initializeRound() has been
	// called for this round. Some consumers (e.g., reward) only act on
	// initialized rounds.
	Initialized bool

	// LastInitialized is RoundsManager.lastInitializedRound() at the time
	// the event fires — the most recent round whose initializeRound() has
	// been called and whose blockHashForRound() is non-zero. When
	// Initialized is true this equals Number; otherwise it trails Number
	// by 1+ rounds. Consumers that need a round whose blockHash is
	// guaranteed available (payment-daemon's ticket creationRound) read
	// this field; consumers that act on the protocol round itself
	// (protocol-daemon initialize/reward calling) read Number.
	LastInitialized RoundNumber

	// BlockHash is RoundsManager.blockHashForRound at the time the event
	// fires. Zero value until set on-chain.
	BlockHash TxHash
}

// String returns a compact representation for logs.
func (r Round) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Round{number=%d start=%d l1=%d len=%d", r.Number, r.StartBlock, r.L1StartBlock, r.Length)
	if r.Initialized {
		b.WriteString(" initialized")
	}
	b.WriteString("}")
	return b.String()
}
