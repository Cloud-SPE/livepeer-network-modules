package chain

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestBlockNumber_BytesAndBigInt(t *testing.T) {
	bn := BlockNumber(0xdeadbeef)
	wantBytes, _ := hex.DecodeString("00000000deadbeef")
	if got := bn.Bytes(); !bytes.Equal(got, wantBytes) {
		t.Errorf("BlockNumber.Bytes() = %x, want %x", got, wantBytes)
	}
	if bn.BigInt().Uint64() != 0xdeadbeef {
		t.Errorf("BlockNumber.BigInt() = %s, want 3735928559", bn.BigInt())
	}
	if bn.String() != "3735928559" {
		t.Errorf("BlockNumber.String() = %s, want 3735928559", bn.String())
	}
}

func TestRoundNumber_BytesAndBigInt(t *testing.T) {
	r := RoundNumber(42)
	wantBytes := []byte{0, 0, 0, 0, 0, 0, 0, 42}
	if got := r.Bytes(); !bytes.Equal(got, wantBytes) {
		t.Errorf("RoundNumber.Bytes() = %x, want %x", got, wantBytes)
	}
	if r.BigInt().Uint64() != 42 {
		t.Errorf("RoundNumber.BigInt() = %s, want 42", r.BigInt())
	}
	if r.String() != "42" {
		t.Errorf("RoundNumber.String() = %s, want 42", r.String())
	}
}

func TestChainID_BigInt(t *testing.T) {
	c := ChainID(42161) // Arbitrum One
	if c.BigInt().Uint64() != 42161 {
		t.Errorf("ChainID.BigInt() = %s, want 42161", c.BigInt())
	}
}

func TestRound_String(t *testing.T) {
	r := Round{
		Number:       100,
		StartBlock:   1000,
		L1StartBlock: 50,
		Length:       6646,
		Initialized:  true,
	}
	got := r.String()
	want := "Round{number=100 start=1000 l1=50 len=6646 initialized}"
	if got != want {
		t.Errorf("Round.String() = %q, want %q", got, want)
	}

	r.Initialized = false
	gotUninit := r.String()
	wantUninit := "Round{number=100 start=1000 l1=50 len=6646}"
	if gotUninit != wantUninit {
		t.Errorf("Round.String() (uninit) = %q, want %q", gotUninit, wantUninit)
	}
}

func TestBlockNumberBytes_Roundtrip(t *testing.T) {
	for _, v := range []uint64{0, 1, 100, 1 << 32, ^uint64(0)} {
		bn := BlockNumber(v)
		b := bn.Bytes()
		if len(b) != 8 {
			t.Errorf("Bytes() len = %d, want 8", len(b))
		}
		got := BlockNumber(0)
		for i := 0; i < 8; i++ {
			got = got<<8 | BlockNumber(b[i])
		}
		if got != bn {
			t.Errorf("roundtrip mismatch: %d -> %x -> %d", v, b, got)
		}
	}
}
