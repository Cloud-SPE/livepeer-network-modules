package ticketbroker

// A stand-in for the on-chain TicketBroker, hand-assembled so the
// simulated-chain tests need no compiler toolchain. It implements the
// three selectors the broker calls:
//
//	usedTickets(bytes32) returns (bool)      — SLOAD of the ticket hash
//	ticketValidityPeriod() returns (uint256) — constant 2
//	redeemWinningTicket(Ticket, bytes, uint256)
//	    reverts when faceValue == 0 (the test's "force a revert" switch)
//	    reverts when the ticket is already used
//	    otherwise SSTORE(hash, 1)
//
// The hash is keccak256 over the same packed layout the real contract
// and internal/types.Ticket.Hash use:
//
//	recipient(20) || sender(20) || faceValue(32) || winProb(32) ||
//	senderNonce(32) || recipientRandHash(32) || auxData(0|64)
//
// so the daemon's own ticket hash is the key the stub records.

import (
	"encoding/binary"
	"fmt"

	"github.com/ethereum/go-ethereum/core/vm"
)

// asm is the smallest assembler that can express the stub: opcodes,
// immediates, and PUSH2 label references resolved at the end.
type asm struct {
	code   []byte
	labels map[string]int
	fixups map[int]string
}

func newASM() *asm { return &asm{labels: map[string]int{}, fixups: map[int]string{}} }

func (a *asm) op(ops ...vm.OpCode) *asm {
	for _, o := range ops {
		a.code = append(a.code, byte(o))
	}
	return a
}

// push emits the shortest PUSHn for v.
func (a *asm) push(v uint64) *asm {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	i := 0
	for i < 7 && buf[i] == 0 {
		i++
	}
	imm := buf[i:]
	a.code = append(a.code, byte(vm.PUSH1)+byte(len(imm)-1))
	a.code = append(a.code, imm...)
	return a
}

func (a *asm) pushBytes(b []byte) *asm {
	a.code = append(a.code, byte(vm.PUSH1)+byte(len(b)-1))
	a.code = append(a.code, b...)
	return a
}

func (a *asm) label(name string) *asm {
	a.labels[name] = len(a.code)
	a.code = append(a.code, byte(vm.JUMPDEST))
	return a
}

// jumpTo emits PUSH2 <label> and the given jump opcode.
func (a *asm) jumpTo(name string, jump vm.OpCode) *asm {
	a.code = append(a.code, byte(vm.PUSH2))
	a.fixups[len(a.code)] = name
	a.code = append(a.code, 0, 0)
	a.code = append(a.code, byte(jump))
	return a
}

func (a *asm) bytes() []byte {
	for at, name := range a.fixups {
		pos, ok := a.labels[name]
		if !ok {
			panic(fmt.Sprintf("asm: unresolved label %q", name))
		}
		binary.BigEndian.PutUint16(a.code[at:], uint16(pos))
	}
	return a.code
}

// stubRuntime is the deployed code.
func stubRuntime() []byte {
	a := newASM()
	// selector = calldata[0:32] >> 224
	a.push(0).op(vm.CALLDATALOAD).push(0xe0).op(vm.SHR)
	a.op(vm.DUP1).pushBytes(ParsedABI.Methods["usedTickets"].ID).op(vm.EQ).jumpTo("used", vm.JUMPI)
	a.op(vm.DUP1).pushBytes(ParsedABI.Methods["redeemWinningTicket"].ID).op(vm.EQ).jumpTo("redeem", vm.JUMPI)
	a.op(vm.DUP1).pushBytes(ParsedABI.Methods["ticketValidityPeriod"].ID).op(vm.EQ).jumpTo("tvp", vm.JUMPI)
	a.push(0).op(vm.DUP1, vm.REVERT)

	// usedTickets(bytes32): return SLOAD(arg0)
	a.label("used")
	a.push(4).op(vm.CALLDATALOAD, vm.SLOAD).push(0).op(vm.MSTORE).push(0x20).push(0).op(vm.RETURN)

	// ticketValidityPeriod(): return 2
	a.label("tvp")
	a.push(2).push(0).op(vm.MSTORE).push(0x20).push(0).op(vm.RETURN)

	// redeemWinningTicket(Ticket, bytes, uint256)
	a.label("redeem")
	// T = calldataload(4) + 4 : start of the tuple             stack: T
	a.push(4).op(vm.CALLDATALOAD).push(4).op(vm.ADD)
	// faceValue (T+64) == 0 → revert                          stack: T
	a.op(vm.DUP1).push(0x40).op(vm.ADD, vm.CALLDATALOAD, vm.ISZERO).jumpTo("revert", vm.JUMPI)
	// mem[0:20]   = recipient   (calldata T+12, 20 bytes)
	a.push(0x14).op(vm.DUP2).push(0x0c).op(vm.ADD).push(0x00).op(vm.CALLDATACOPY)
	// mem[20:40]  = sender      (calldata T+44, 20 bytes)
	a.push(0x14).op(vm.DUP2).push(0x2c).op(vm.ADD).push(0x14).op(vm.CALLDATACOPY)
	// mem[40:168] = faceValue, winProb, senderNonce, recipientRandHash (T+64, 128 bytes)
	a.push(0x80).op(vm.DUP2).push(0x40).op(vm.ADD).push(0x28).op(vm.CALLDATACOPY)
	// A = T + calldataload(T+192) : auxData length word        stack: A
	a.op(vm.DUP1).push(0xc0).op(vm.ADD, vm.CALLDATALOAD, vm.ADD)
	// len = calldataload(A)                                    stack: A len
	a.op(vm.DUP1, vm.CALLDATALOAD)
	// mem[168:168+len] = calldata[A+32 : A+32+len]
	a.op(vm.DUP1, vm.DUP3).push(0x20).op(vm.ADD).push(0xa8).op(vm.CALLDATACOPY)
	// hash = keccak256(mem[0 : 168+len])                       stack: A hash
	a.push(0xa8).op(vm.ADD).push(0).op(vm.KECCAK256)
	a.op(vm.SWAP1, vm.POP) //                                   stack: hash
	// already used → revert
	a.op(vm.DUP1, vm.SLOAD).jumpTo("revert", vm.JUMPI)
	// SSTORE(hash, 1)
	a.push(1).op(vm.SWAP1, vm.SSTORE, vm.STOP)

	a.label("revert")
	a.push(0).op(vm.DUP1, vm.REVERT)
	return a.bytes()
}

// stubInitCode wraps the runtime in a constructor that returns it.
func stubInitCode() []byte {
	rt := stubRuntime()
	a := newASM()
	// CODECOPY(dest=0, offset=<after init>, size=len(rt)); RETURN(0, len)
	// The init prologue length is fixed once its own pushes are sized:
	// PUSH2 len, PUSH2 off, PUSH1 0, CODECOPY, PUSH2 len, PUSH1 0, RETURN
	const prologue = 3 + 3 + 2 + 1 + 3 + 2 + 1
	a.code = append(a.code, byte(vm.PUSH2), byte(len(rt)>>8), byte(len(rt)))
	a.code = append(a.code, byte(vm.PUSH2), byte(prologue>>8), byte(prologue))
	a.code = append(a.code, byte(vm.PUSH1), 0, byte(vm.CODECOPY))
	a.code = append(a.code, byte(vm.PUSH2), byte(len(rt)>>8), byte(len(rt)))
	a.code = append(a.code, byte(vm.PUSH1), 0, byte(vm.RETURN))
	if len(a.code) != prologue {
		panic(fmt.Sprintf("stub prologue is %d bytes, expected %d", len(a.code), prologue))
	}
	return append(a.code, rt...)
}
