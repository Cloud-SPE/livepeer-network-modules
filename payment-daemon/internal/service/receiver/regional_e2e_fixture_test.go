package receiver

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"os"
	"testing"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/revenuereport"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/types"
	"github.com/ethereum/go-ethereum/common"
	"google.golang.org/grpc"
)

type e2eRevenueChain struct {
	entries map[common.Hash]*types.RedemptionInclusion
	through int64
}

func (c *e2eRevenueChain) RevenueCoverage(context.Context) (*types.RevenueCoverage, error) {
	return &types.RevenueCoverage{Head: 2000, HeadHash: common.HexToHash("0xabcd").Bytes(), FinalizedBlock: 1996, FinalizedBlockHash: common.HexToHash("0xdef0").Bytes(), CompleteThroughRound: c.through, ObservedAt: time.Now()}, nil
}
func (*e2eRevenueChain) VerifyRevenueCoverage(context.Context, *types.RevenueCoverage) error {
	return nil
}
func (c *e2eRevenueChain) InclusionForTransaction(_ context.Context, h common.Hash) (*types.RedemptionInclusion, error) {
	e := c.entries[h]
	if e == nil {
		return nil, errors.New("unknown synthetic transaction")
	}
	return e, nil
}
func (*e2eRevenueChain) ConfirmedInclusion(context.Context, []byte) (*types.RedemptionInclusion, error) {
	return nil, errors.New("unconfirmed synthetic transaction")
}

// Only the chain boundary is synthetic. The durable receiver store, inclusion
// aggregation and PayeeDaemon gRPC service are production implementations.
func TestRegionalE2EReceiver(t *testing.T) {
	path := os.Getenv("REGIONAL_E2E_RECEIVER")
	if path == "" {
		t.Skip("explicit e2e fixture process only")
	}
	var cfg struct {
		Store, Socket, Source, Payee string
		Count                        int
		ZeroRoundRevenue             int64
		FaceValue                    int64
		Round, Through               int64
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chain := &e2eRevenueChain{entries: map[common.Hash]*types.RedemptionInclusion{}, through: cfg.Through}
	count := cfg.Count
	if cfg.ZeroRoundRevenue > 0 {
		count++
	}
	for i := 1; i <= count; i++ {
		round, value := cfg.Round, cfg.FaceValue
		if i > cfg.Count {
			round, value = 114, cfg.ZeroRoundRevenue
		}
		hash := make([]byte, 32)
		binary.BigEndian.PutUint64(hash[24:], uint64(i))
		tx := common.BytesToHash(hash)
		evidence := &types.RedemptionInclusion{TxHash: tx.Bytes(), BlockNumber: uint64(100 + i), BlockHash: common.HexToHash("0xabc").Bytes(), Round: round, ObservedHead: 2000, CheckedAt: time.Now()}
		ticket := &store.SignedTicket{FaceValue: big.NewInt(value), CreationRound: round - 1}
		if _, err = st.EnqueueRedemption(hash, ticket); err != nil {
			t.Fatal(err)
		}
		if err = st.MarkRedeemedWithInclusion(hash, ticket, evidence); err != nil {
			t.Fatal(err)
		}
		chain.entries[tx] = evidence
	}
	svc := New(st, Config{Recipient: common.HexToAddress(cfg.Payee).Bytes(), ChainID: 42161, SettlementDomainID: cfg.Source}, slog.Default())
	svc.RevenueReporter = (revenuereport.Reporter{Store: st, Chain: chain}).Round
	listener, err := net.Listen("unix", cfg.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := grpc.NewServer()
	defer server.Stop()
	pb.RegisterPayeeDaemonServer(server, svc)
	if err = server.Serve(listener); err != nil {
		t.Fatal(err)
	}
}
