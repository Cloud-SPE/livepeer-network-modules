package serviceregistry

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/txintent"
)

type stubRegistry struct {
	addr     chain.Address
	calldata []byte
	err      error
	uri      string
}

func (s *stubRegistry) Address() chain.Address { return s.addr }
func (s *stubRegistry) PackSetServiceURI(uri string) ([]byte, error) {
	s.uri = uri
	if s.err != nil {
		return nil, s.err
	}
	if s.calldata != nil {
		return s.calldata, nil
	}
	return []byte{0xde, 0xad, 0xbe, 0xef}, nil
}

type stubSubmitter struct {
	params txintent.Params
	err    error
}

func (s *stubSubmitter) Submit(_ context.Context, p txintent.Params) (txintent.IntentID, error) {
	s.params = p
	if s.err != nil {
		return txintent.IntentID{}, s.err
	}
	return txintent.ComputeID(p.Kind, p.KeyParams), nil
}

func TestNewValidates(t *testing.T) {
	addr := common.HexToAddress("0x0000000000000000000000000000000000001234")
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected missing deps error")
	}
	if _, err := New(Config{Registry: &stubRegistry{addr: addr}, TxIntent: &stubSubmitter{}}); err == nil {
		t.Fatal("expected missing gas limit error")
	}
}

func TestSetServiceURISubmitsTxIntent(t *testing.T) {
	addr := common.HexToAddress("0x0000000000000000000000000000000000001234")
	reg := &stubRegistry{addr: addr}
	sub := &stubSubmitter{}
	svc, err := New(Config{Registry: reg, TxIntent: sub, GasLimit: 123_456})
	if err != nil {
		t.Fatal(err)
	}

	id, err := svc.SetServiceURI(context.Background(), " https://orch.example.com ")
	if err != nil {
		t.Fatal(err)
	}
	if reg.uri != "https://orch.example.com" {
		t.Fatalf("normalized uri = %q", reg.uri)
	}
	if got, want := sub.params.Kind, "SetServiceURI"; got != want {
		t.Fatalf("Kind = %q; want %q", got, want)
	}
	if sub.params.To != addr {
		t.Fatalf("To = %s; want %s", sub.params.To.Hex(), addr.Hex())
	}
	if sub.params.GasLimit != 123_456 {
		t.Fatalf("GasLimit = %d", sub.params.GasLimit)
	}
	if sub.params.Value == nil || sub.params.Value.Cmp(new(big.Int)) != 0 {
		t.Fatalf("Value = %v; want 0", sub.params.Value)
	}
	if sub.params.Metadata["service_uri"] != "https://orch.example.com" {
		t.Fatalf("metadata = %#v", sub.params.Metadata)
	}
	if id != txintent.ComputeID(sub.params.Kind, sub.params.KeyParams) {
		t.Fatal("unexpected intent id")
	}
}

func TestSetServiceURIRejectsBadURI(t *testing.T) {
	addr := common.HexToAddress("0x0000000000000000000000000000000000001234")
	svc, err := New(Config{Registry: &stubRegistry{addr: addr}, TxIntent: &stubSubmitter{}, GasLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetServiceURI(context.Background(), "ftp://bad.example.com"); err == nil {
		t.Fatal("expected invalid URI error")
	}
}

func TestSetServiceURIPropagatesPackError(t *testing.T) {
	addr := common.HexToAddress("0x0000000000000000000000000000000000001234")
	svc, err := New(Config{
		Registry: &stubRegistry{addr: addr, err: errors.New("boom")},
		TxIntent: &stubSubmitter{},
		GasLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetServiceURI(context.Background(), "https://orch.example.com"); err == nil {
		t.Fatal("expected pack error")
	}
}
