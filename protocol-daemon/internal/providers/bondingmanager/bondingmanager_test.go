package bondingmanager

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	chaintesting "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
)

func TestPackRewardWithHint(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FB01"))
	prev := common.HexToAddress("0x00000000000000000000000000000000000000AA")
	next := common.HexToAddress("0x00000000000000000000000000000000000000BB")
	data, err := b.PackRewardWithHint(prev, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 4+64 {
		t.Fatalf("PackRewardWithHint returned %d bytes, want %d", len(data), 4+64)
	}
	if !bytes.Equal(data[:4], selectorRewardWithHint) {
		t.Fatal("rewardWithHint selector mismatch")
	}
	// Verify the address slots round-trip via the rightmost-20-bytes
	// extraction the chain-commons binding uses.
	var p, n chain.Address
	copy(p[:], data[4+12:4+32])
	copy(n[:], data[4+32+12:4+64])
	if p != prev {
		t.Fatalf("prev decode = %s; want %s", p.Hex(), prev.Hex())
	}
	if n != next {
		t.Fatalf("next decode = %s; want %s", n.Hex(), next.Hex())
	}
}

func TestDecodeRewardEvent(t *testing.T) {
	transcoder := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	amount := big.NewInt(7_777_777)
	var topic1 chain.TxHash
	copy(topic1[12:], transcoder[:])
	data := make([]byte, 32)
	amount.FillBytes(data)

	log := ethtypes.Log{
		Topics: []chain.TxHash{EventReward, topic1},
		Data:   data,
	}

	got, gotAmt, ok := DecodeRewardEvent(log)
	if !ok {
		t.Fatal("DecodeRewardEvent ok=false; want true")
	}
	if got != transcoder {
		t.Fatalf("transcoder = %s; want %s", got.Hex(), transcoder.Hex())
	}
	if gotAmt.Cmp(amount) != 0 {
		t.Fatalf("amount = %s; want %s", gotAmt, amount)
	}
}

func TestDecodeRewardEventReject(t *testing.T) {
	// Wrong topic0.
	other := common.HexToAddress("0x12")
	var topic1 chain.TxHash
	copy(topic1[12:], other[:])
	log := ethtypes.Log{Topics: []chain.TxHash{{0xFF}, topic1}, Data: make([]byte, 32)}
	if _, _, ok := DecodeRewardEvent(log); ok {
		t.Fatal("DecodeRewardEvent: expected reject on wrong topic0")
	}
	// Missing topic1.
	log2 := ethtypes.Log{Topics: []chain.TxHash{EventReward}, Data: make([]byte, 32)}
	if _, _, ok := DecodeRewardEvent(log2); ok {
		t.Fatal("DecodeRewardEvent: expected reject on missing topic1")
	}
	// Short data.
	log3 := ethtypes.Log{Topics: []chain.TxHash{EventReward, topic1}, Data: []byte{0x01}}
	if _, _, ok := DecodeRewardEvent(log3); ok {
		t.Fatal("DecodeRewardEvent: expected reject on short data")
	}
}

func TestFindRewardForTranscoder(t *testing.T) {
	transcoder := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	other := common.HexToAddress("0x00000000000000000000000000000000000000A2")
	amt := big.NewInt(123)

	var topicT, topicO chain.TxHash
	copy(topicT[12:], transcoder[:])
	copy(topicO[12:], other[:])

	logs := []ethtypes.Log{
		{Topics: []chain.TxHash{EventReward, topicO}, Data: encodeAmt(big.NewInt(99))},
		{Topics: []chain.TxHash{{0xAA}}, Data: nil}, // unrelated
		{Topics: []chain.TxHash{EventReward, topicT}, Data: encodeAmt(amt)},
	}
	got, ok := FindRewardForTranscoder(logs, transcoder)
	if !ok {
		t.Fatal("FindRewardForTranscoder = false; want true")
	}
	if got.Cmp(amt) != 0 {
		t.Fatalf("amt = %s; want %s", got, amt)
	}

	// Not found.
	if _, ok := FindRewardForTranscoder(logs, common.HexToAddress("0xBB")); ok {
		t.Fatal("FindRewardForTranscoder for missing addr should be false")
	}
}

func encodeAmt(v *big.Int) []byte {
	out := make([]byte, 32)
	v.FillBytes(out)
	return out
}
