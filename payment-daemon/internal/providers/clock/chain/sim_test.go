package chain

// The real Clock over the real chain-commons poller against a simulated
// chain holding hand-assembled stand-ins for RoundsManager and
// BondingManager (no compiler toolchain, same approach as the ticket
// broker's stub). The stubs answer every selector the poller and the
// bindings issue and expose setters so the test can move the chain:
//
//	RoundsManager:  currentRound()→slot0  lastInitializedRound()→slot1
//	                roundLength()→1  currentRoundStartBlock()→0
//	                currentRoundInitialized()→true
//	                blockHashForRound(r)→bytes32(r)
//	                setRounds(cur,last) writes slot0, slot1
//	BondingManager: getTranscoderPoolSize()→slot0  setPoolSize(n)

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/vm"

	cchain "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/controller"
	chaintesting "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing/simchain"
)

// asm is the smallest assembler that can express the stubs: opcodes,
// immediates, and PUSH2 label references resolved at the end. A copy
// of the ticket broker's test assembler; test-only code, kept local so
// the production tree carries nothing for it.
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

// returnConst emits: return the 32-byte constant v.
func (a *asm) returnConst(v uint64) *asm {
	return a.push(v).push(0).op(vm.MSTORE).push(0x20).push(0).op(vm.RETURN)
}

// returnSlot emits: return SLOAD(slot).
func (a *asm) returnSlot(slot uint64) *asm {
	return a.push(slot).op(vm.SLOAD).push(0).op(vm.MSTORE).push(0x20).push(0).op(vm.RETURN)
}

func (a *asm) dispatch(sig, label string) *asm {
	return a.op(vm.DUP1).pushBytes(sel(sig)).op(vm.EQ).jumpTo(label, vm.JUMPI)
}

var (
	selSetRounds   = sel("setRounds(uint256,uint256)")
	selSetPoolSize = sel("setPoolSize(uint256)")
)

func roundsManagerRuntime() []byte {
	a := newASM()
	a.push(0).op(vm.CALLDATALOAD).push(0xe0).op(vm.SHR)
	a.dispatch("currentRound()", "cur")
	a.dispatch("lastInitializedRound()", "last")
	a.dispatch("roundLength()", "len")
	a.dispatch("currentRoundStartBlock()", "start")
	a.dispatch("currentRoundInitialized()", "init")
	a.dispatch("blockHashForRound(uint256)", "hash")
	a.dispatch("setRounds(uint256,uint256)", "set")
	a.push(0).op(vm.DUP1, vm.REVERT)
	a.label("cur").returnSlot(0)
	a.label("last").returnSlot(1)
	a.label("len").returnConst(1)
	a.label("start").returnConst(0)
	a.label("init").returnConst(1)
	// blockHashForRound(r): return r as bytes32
	a.label("hash").push(4).op(vm.CALLDATALOAD).push(0).op(vm.MSTORE).push(0x20).push(0).op(vm.RETURN)
	// setRounds(cur, last): SSTORE(0, arg0); SSTORE(1, arg1)
	a.label("set").push(4).op(vm.CALLDATALOAD).push(0).op(vm.SSTORE).push(0x24).op(vm.CALLDATALOAD).push(1).op(vm.SSTORE, vm.STOP)
	return a.bytes()
}

func bondingManagerRuntime() []byte {
	a := newASM()
	a.push(0).op(vm.CALLDATALOAD).push(0xe0).op(vm.SHR)
	a.dispatch("getTranscoderPoolSize()", "size")
	a.dispatch("setPoolSize(uint256)", "set")
	a.push(0).op(vm.DUP1, vm.REVERT)
	a.label("size").returnSlot(0)
	a.label("set").push(4).op(vm.CALLDATALOAD).push(0).op(vm.SSTORE, vm.STOP)
	return a.bytes()
}

// initCode wraps a runtime in a constructor that returns it.
func initCode(rt []byte) []byte {
	const prologue = 3 + 3 + 2 + 1 + 3 + 2 + 1
	var code []byte
	code = append(code, byte(vm.PUSH2), byte(len(rt)>>8), byte(len(rt)))
	code = append(code, byte(vm.PUSH2), byte(prologue>>8), byte(prologue))
	code = append(code, byte(vm.PUSH1), 0, byte(vm.CODECOPY))
	code = append(code, byte(vm.PUSH2), byte(len(rt)>>8), byte(len(rt)))
	code = append(code, byte(vm.PUSH1), 0, byte(vm.RETURN))
	if len(code) != prologue {
		panic(fmt.Sprintf("stub prologue is %d bytes, expected %d", len(code), prologue))
	}
	return append(code, rt...)
}

// call sends a state-changing call to a stub and mines it.
func call(t *testing.T, sim *simchain.Chain, to cchain.Address, selector []byte, args ...uint64) {
	t.Helper()
	data := append([]byte(nil), selector...)
	for _, v := range args {
		data = append(data, uintSlot(v)...)
	}
	ctx := context.Background()
	tx, err := sim.NewDynamicFeeTx(ctx, sim.Accounts[0], &to, new(big.Int), 100_000, data)
	if err != nil {
		t.Fatalf("build tx: %v", err)
	}
	signed, err := sim.SignTx(sim.Accounts[0], tx)
	if err != nil {
		t.Fatalf("sign tx: %v", err)
	}
	if err := sim.RPC().SendTransaction(ctx, signed); err != nil {
		t.Fatalf("send tx: %v", err)
	}
	sim.Commit()
	rcpt, err := sim.RPC().TransactionReceipt(ctx, signed.Hash())
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if rcpt.Status != 1 {
		t.Fatalf("stub call %x reverted", selector)
	}
}

func TestClock_SimulatedChain(t *testing.T) {
	sim := simchain.New(t)
	ctx := context.Background()
	rmAddr, _, err := sim.Deploy(ctx, sim.Accounts[0], initCode(roundsManagerRuntime()), "")
	if err != nil {
		t.Fatalf("deploy RoundsManager stub: %v", err)
	}
	bmAddr, _, err := sim.Deploy(ctx, sim.Accounts[0], initCode(bondingManagerRuntime()), "")
	if err != nil {
		t.Fatalf("deploy BondingManager stub: %v", err)
	}
	call(t, sim, rmAddr, selSetRounds, 3000, 2999) // current 3000, last initialized 2999
	call(t, sim, bmAddr, selSetPoolSize, 25)

	ctrl := chaintesting.NewFakeController(controller.Addresses{RoundsManager: rmAddr, BondingManager: bmAddr}, nil)
	c, err := New(ctx, Config{RefreshInterval: 5 * time.Millisecond}, sim.RPC(), ctrl)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.LastInitializedRound() != 2999 {
		t.Fatalf("initial LastInitializedRound = %d; want 2999", c.LastInitializedRound())
	}
	if h := c.LastInitializedL1BlockHash(); new(big.Int).SetBytes(h).Int64() != 2999 {
		t.Fatalf("initial hash = %x; want bytes32(2999)", h)
	}
	if c.GetTranscoderPoolSize().Int64() != 25 {
		t.Fatalf("initial pool = %s; want 25", c.GetTranscoderPoolSize())
	}
	headAtStart := c.LastSeenL1Block().Int64()
	if headAtStart == 0 {
		t.Fatal("head must be non-zero after the deploys")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := c.Start(runCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop()

	// initializeRound lands for round 3000: lastInitializedRound moves,
	// the hash follows, the head advanced by the mined block.
	call(t, sim, rmAddr, selSetRounds, 3000, 3000)
	waitFor(t, "round 3000 initialized", func() bool { return c.LastInitializedRound() == 3000 })
	if h := c.LastInitializedL1BlockHash(); new(big.Int).SetBytes(h).Int64() != 3000 {
		t.Fatalf("hash after init = %x; want bytes32(3000)", h)
	}
	waitFor(t, "head advance", func() bool { return c.LastSeenL1Block().Int64() > headAtStart })

	// An orchestrator joins the active set.
	call(t, sim, bmAddr, selSetPoolSize, 26)
	waitFor(t, "pool size 26", func() bool { return c.GetTranscoderPoolSize().Int64() == 26 })

	// A new round becomes current before it is initialized: the clock
	// stays on the last initialized round, as the ticket path requires.
	call(t, sim, rmAddr, selSetRounds, 3001, 3000)
	call(t, sim, bmAddr, selSetPoolSize, 27) // observable proof a poll happened after the round moved
	waitFor(t, "pool size 27", func() bool { return c.GetTranscoderPoolSize().Int64() == 27 })
	if c.LastInitializedRound() != 3000 {
		t.Fatalf("clock followed currentRound instead of lastInitializedRound: %d", c.LastInitializedRound())
	}
}
