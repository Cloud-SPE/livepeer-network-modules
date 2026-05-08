package reward

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/txintent"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/providers/bondingmanager"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/repo/poolhints"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
)

// stubBondingManager is a reward.BondingManager test stub.
type stubBondingManager struct {
	mu               sync.Mutex
	addr             chain.Address
	transcoder       bondingmanager.TranscoderInfo
	transcoderErr    error
	pool             []chain.Address
	getFirstCalls    int
	getNextCalls     int
	getTranscoderErr error
	walkErr          error
}

func (s *stubBondingManager) Address() chain.Address { return s.addr }

func (s *stubBondingManager) GetTranscoder(_ context.Context, _ chain.Address) (bondingmanager.TranscoderInfo, error) {
	if s.getTranscoderErr != nil {
		return bondingmanager.TranscoderInfo{}, s.getTranscoderErr
	}
	return s.transcoder, s.transcoderErr
}

func (s *stubBondingManager) GetFirstTranscoderInPool(_ context.Context) (chain.Address, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getFirstCalls++
	if s.walkErr != nil {
		return chain.Address{}, s.walkErr
	}
	if len(s.pool) == 0 {
		return chain.Address{}, nil
	}
	return s.pool[0], nil
}

func (s *stubBondingManager) GetNextTranscoderInPool(_ context.Context, addr chain.Address) (chain.Address, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getNextCalls++
	if s.walkErr != nil {
		return chain.Address{}, s.walkErr
	}
	for i, p := range s.pool {
		if p == addr {
			if i+1 >= len(s.pool) {
				return chain.Address{}, nil
			}
			return s.pool[i+1], nil
		}
	}
	return chain.Address{}, nil
}

func (s *stubBondingManager) PackRewardWithHint(prev, next chain.Address) ([]byte, error) {
	out := make([]byte, 4+64)
	copy(out[4+12:], prev[:])
	copy(out[4+32+12:], next[:])
	return out, nil
}

// stubSubmitter mimics chain-commons.txintent.Manager's submit semantics.
type stubSubmitter struct {
	mu        sync.Mutex
	submitted []txintent.Params
	failNext  error
	idMap     map[string]txintent.IntentID
}

func newStubSubmitter() *stubSubmitter {
	return &stubSubmitter{idMap: map[string]txintent.IntentID{}}
}

func (s *stubSubmitter) Submit(_ context.Context, p txintent.Params) (txintent.IntentID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext != nil {
		err := s.failNext
		s.failNext = nil
		return txintent.IntentID{}, err
	}
	id := txintent.ComputeID(p.Kind, p.KeyParams)
	if _, ok := s.idMap[id.Hex()]; ok {
		return id, nil
	}
	s.idMap[id.Hex()] = id
	s.submitted = append(s.submitted, p)
	return id, nil
}

func (s *stubSubmitter) Status(_ context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
	return txintent.TxIntent{ID: id}, nil
}

func (s *stubSubmitter) Wait(_ context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
	return txintent.TxIntent{ID: id, Status: txintent.StatusConfirmed}, nil
}

func newCache(t *testing.T) PoolHintsCache {
	c, err := poolhints.New(store.Memory())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustNewSvc(t *testing.T, bm BondingManager, sub TxSubmitter, cache PoolHintsCache, orch chain.Address) *Service {
	t.Helper()
	svc, err := New(Config{
		BondingManager: bm,
		TxIntent:       sub,
		Cache:          cache,
		OrchAddress:    orch,
		GasLimit:       1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestNewValidates(t *testing.T) {
	cache := newCache(t)
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error empty cfg")
	}
	if _, err := New(Config{BondingManager: &stubBondingManager{}}); err == nil {
		t.Fatal("expected error missing TxIntent")
	}
	if _, err := New(Config{BondingManager: &stubBondingManager{}, TxIntent: newStubSubmitter()}); err == nil {
		t.Fatal("expected error missing Cache")
	}
	if _, err := New(Config{BondingManager: &stubBondingManager{}, TxIntent: newStubSubmitter(), Cache: cache}); err == nil {
		t.Fatal("expected error missing OrchAddress")
	}
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	if _, err := New(Config{BondingManager: &stubBondingManager{}, TxIntent: newStubSubmitter(), Cache: cache, OrchAddress: orch}); err == nil {
		t.Fatal("expected error missing GasLimit")
	}
}

func TestTryRewardEligibleSubmits(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	other1 := common.HexToAddress("0x00000000000000000000000000000000000000B1")
	other2 := common.HexToAddress("0x00000000000000000000000000000000000000B2")

	bm := &stubBondingManager{
		addr: common.HexToAddress("0x000000000000000000000000000000000000FB01"),
		transcoder: bondingmanager.TranscoderInfo{
			Active:          true,
			ActivationRound: 1,
			LastRewardRound: 99,
			Address:         orch,
		},
		pool: []chain.Address{other1, orch, other2},
	}
	sub := newStubSubmitter()
	cache := newCache(t)
	svc := mustNewSvc(t, bm, sub, cache, orch)

	res, err := svc.TryReward(context.Background(), chain.Round{Number: 100})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skip != nil {
		t.Fatalf("expected submit, got skip: %+v", res.Skip)
	}
	if res.IntentID == (txintent.IntentID{}) {
		t.Fatal("expected nonzero IntentID")
	}
	if len(sub.submitted) != 1 {
		t.Fatalf("submitted len = %d; want 1", len(sub.submitted))
	}
	st := svc.Status()
	if st.LastEligibility == nil || !st.LastEligibility.Eligible {
		t.Fatal("expected Eligible=true in Status")
	}
}

func TestTryRewardSkippedInactive(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	bm := &stubBondingManager{
		addr: common.HexToAddress("0x000000000000000000000000000000000000FB01"),
		transcoder: bondingmanager.TranscoderInfo{
			Active:          false,
			LastRewardRound: 0,
		},
	}
	sub := newStubSubmitter()
	svc := mustNewSvc(t, bm, sub, newCache(t), orch)

	res, err := svc.TryReward(context.Background(), chain.Round{Number: 100})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skip == nil {
		t.Fatal("expected typed Skip for ineligible orch")
	}
	if res.Skip.Code != SkipCodeTranscoderInactive {
		t.Fatalf("Skip.Code = %d; want SkipCodeTranscoderInactive (%d)",
			res.Skip.Code, SkipCodeTranscoderInactive)
	}
	if res.Skip.Reason != "transcoder is not active at this round" {
		t.Fatalf("Skip.Reason = %q", res.Skip.Reason)
	}
	if res.IntentID != (txintent.IntentID{}) {
		t.Fatal("expected zero IntentID alongside Skip")
	}
	if len(sub.submitted) != 0 {
		t.Fatal("submit should not be called for ineligible")
	}
	st := svc.Status()
	if st.LastEligibility == nil || st.LastEligibility.Eligible {
		t.Fatal("expected Eligible=false in Status")
	}
}

func TestTryRewardSkippedAlreadyRewarded(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	bm := &stubBondingManager{
		addr: common.HexToAddress("0x000000000000000000000000000000000000FB01"),
		transcoder: bondingmanager.TranscoderInfo{
			Active: true, ActivationRound: 1, LastRewardRound: 100,
		},
	}
	sub := newStubSubmitter()
	svc := mustNewSvc(t, bm, sub, newCache(t), orch)

	res, err := svc.TryReward(context.Background(), chain.Round{Number: 100})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skip == nil {
		t.Fatal("expected typed Skip for already-rewarded orch")
	}
	if res.Skip.Code != SkipCodeAlreadyRewarded {
		t.Fatalf("Skip.Code = %d; want SkipCodeAlreadyRewarded (%d)",
			res.Skip.Code, SkipCodeAlreadyRewarded)
	}
	if res.Skip.Reason != "already rewarded this round" {
		t.Fatalf("Skip.Reason = %q", res.Skip.Reason)
	}
	if len(sub.submitted) != 0 {
		t.Fatal("submit should not be called when already rewarded")
	}
	st := svc.Status()
	if st.LastEligibility.Reason == "" {
		t.Fatal("expected reason set")
	}
}

func TestTryRewardGetTranscoderError(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	bm := &stubBondingManager{
		addr:             common.HexToAddress("0x000000000000000000000000000000000000FB01"),
		getTranscoderErr: errors.New("rpc down"),
	}
	sub := newStubSubmitter()
	svc := mustNewSvc(t, bm, sub, newCache(t), orch)

	if _, err := svc.TryReward(context.Background(), chain.Round{Number: 100}); err == nil {
		t.Fatal("expected error")
	}
}

func TestTryRewardPoolWalkError(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	bm := &stubBondingManager{
		addr:       common.HexToAddress("0x000000000000000000000000000000000000FB01"),
		transcoder: bondingmanager.TranscoderInfo{Active: true, ActivationRound: 1, LastRewardRound: 0},
		walkErr:    errors.New("walk failed"),
	}
	sub := newStubSubmitter()
	svc := mustNewSvc(t, bm, sub, newCache(t), orch)
	if _, err := svc.TryReward(context.Background(), chain.Round{Number: 100}); err == nil {
		t.Fatal("expected error")
	}
}

func TestTryRewardSubmitError(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	bm := &stubBondingManager{
		addr:       common.HexToAddress("0x000000000000000000000000000000000000FB01"),
		transcoder: bondingmanager.TranscoderInfo{Active: true, ActivationRound: 1, LastRewardRound: 0},
		pool:       []chain.Address{orch},
	}
	sub := newStubSubmitter()
	sub.failNext = errors.New("submit failed")
	svc := mustNewSvc(t, bm, sub, newCache(t), orch)
	if _, err := svc.TryReward(context.Background(), chain.Round{Number: 100}); err == nil {
		t.Fatal("expected error")
	}
}

func TestPoolHintCacheSecondCallSkipsWalk(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	other1 := common.HexToAddress("0x00000000000000000000000000000000000000B1")
	other2 := common.HexToAddress("0x00000000000000000000000000000000000000B2")
	bm := &stubBondingManager{
		addr:       common.HexToAddress("0x000000000000000000000000000000000000FB01"),
		transcoder: bondingmanager.TranscoderInfo{Active: true, ActivationRound: 1, LastRewardRound: 0},
		pool:       []chain.Address{other1, orch, other2},
	}
	sub := newStubSubmitter()
	cache := newCache(t)
	svc := mustNewSvc(t, bm, sub, cache, orch)

	// First call: pool walk happens.
	if _, err := svc.TryReward(context.Background(), chain.Round{Number: 100}); err != nil {
		t.Fatal(err)
	}
	firstWalkCalls := bm.getFirstCalls
	nextWalkCalls := bm.getNextCalls
	if firstWalkCalls != 1 {
		t.Fatalf("first call: getFirstCalls = %d; want 1", firstWalkCalls)
	}

	// Second call (same round, same orch): cache hit; no walk.
	// Mark transcoder eligible again.
	bm.transcoder.LastRewardRound = 0
	// Use a fresh submit attempt — the txintent is idempotent so calling
	// twice is safe.
	if _, err := svc.TryReward(context.Background(), chain.Round{Number: 100}); err != nil {
		t.Fatal(err)
	}
	if bm.getFirstCalls != firstWalkCalls {
		t.Fatalf("second call: pool walk re-ran (getFirstCalls = %d; want %d)",
			bm.getFirstCalls, firstWalkCalls)
	}
	if bm.getNextCalls != nextWalkCalls {
		t.Fatalf("second call: getNextCalls bumped (got %d; want %d)",
			bm.getNextCalls, nextWalkCalls)
	}
}

func TestWalkPoolOrchNotInPool(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	other := common.HexToAddress("0x00000000000000000000000000000000000000B1")
	bm := &stubBondingManager{
		addr: common.HexToAddress("0x000000000000000000000000000000000000FB01"),
		pool: []chain.Address{other},
	}
	sub := newStubSubmitter()
	svc := mustNewSvc(t, bm, sub, newCache(t), orch)
	hints, err := svc.walkPool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hints.IsZero() {
		t.Fatalf("expected zero hints when orch not in pool, got %+v", hints)
	}
}

func TestWalkPoolOrchAtHead(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	other := common.HexToAddress("0x00000000000000000000000000000000000000B1")
	bm := &stubBondingManager{
		addr: common.HexToAddress("0x000000000000000000000000000000000000FB01"),
		pool: []chain.Address{orch, other},
	}
	sub := newStubSubmitter()
	svc := mustNewSvc(t, bm, sub, newCache(t), orch)
	hints, err := svc.walkPool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hints.Prev != (chain.Address{}) {
		t.Fatalf("Prev = %s; want zero", hints.Prev)
	}
	if hints.Next != other {
		t.Fatalf("Next = %s; want %s", hints.Next, other)
	}
}

func TestWalkPoolOrchAtTail(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	other := common.HexToAddress("0x00000000000000000000000000000000000000B1")
	bm := &stubBondingManager{
		addr: common.HexToAddress("0x000000000000000000000000000000000000FB01"),
		pool: []chain.Address{other, orch},
	}
	sub := newStubSubmitter()
	svc := mustNewSvc(t, bm, sub, newCache(t), orch)
	hints, err := svc.walkPool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hints.Prev != other {
		t.Fatalf("Prev = %s; want %s", hints.Prev, other)
	}
	if hints.Next != (chain.Address{}) {
		t.Fatalf("Next = %s; want zero", hints.Next)
	}
}

func TestRunStopsOnContext(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	bm := &stubBondingManager{
		addr:       common.HexToAddress("0x000000000000000000000000000000000000FB01"),
		transcoder: bondingmanager.TranscoderInfo{Active: true, ActivationRound: 1, LastRewardRound: 99},
		pool:       []chain.Address{orch},
	}
	sub := newStubSubmitter()
	svc := mustNewSvc(t, bm, sub, newCache(t), orch)
	rc := &stubRoundClock{rounds: make(chan chain.Round, 1)}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx, rc) }()

	rc.rounds <- chain.Round{Number: 100}
	time.Sleep(50 * time.Millisecond)
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v; want context.Canceled", err)
	}
}

func TestRunRecordsErrorOutcome(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	bm := &stubBondingManager{
		addr:             common.HexToAddress("0x000000000000000000000000000000000000FB01"),
		getTranscoderErr: errors.New("rpc down"),
	}
	sub := newStubSubmitter()
	svc := mustNewSvc(t, bm, sub, newCache(t), orch)
	rc := &stubRoundClock{rounds: make(chan chain.Round, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx, rc) }()
	rc.rounds <- chain.Round{Number: 100}
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	st := svc.Status()
	if st.LastError == "" {
		t.Fatal("expected LastError set")
	}
}

func TestRunStopsOnClosedChannel(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	bm := &stubBondingManager{
		addr:       common.HexToAddress("0x000000000000000000000000000000000000FB01"),
		transcoder: bondingmanager.TranscoderInfo{Active: false},
	}
	sub := newStubSubmitter()
	svc := mustNewSvc(t, bm, sub, newCache(t), orch)
	rc := &stubRoundClock{rounds: make(chan chain.Round)}
	close(rc.rounds)
	if err := svc.Run(context.Background(), rc); err != nil {
		t.Fatalf("Run err = %v; want nil", err)
	}
}

func TestRunSubscribeError(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	bm := &stubBondingManager{addr: common.HexToAddress("0x000000000000000000000000000000000000FB01")}
	sub := newStubSubmitter()
	svc := mustNewSvc(t, bm, sub, newCache(t), orch)
	rc := &stubRoundClock{subErr: errors.New("subscribe failed")}
	if err := svc.Run(context.Background(), rc); err == nil {
		t.Fatal("expected subscribe error")
	}
}

func TestParseEarnings(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	bm := &stubBondingManager{addr: common.HexToAddress("0x000000000000000000000000000000000000FB01")}
	sub := newStubSubmitter()
	svc := mustNewSvc(t, bm, sub, newCache(t), orch)

	// No matching log:
	if _, ok := svc.ParseEarnings([]ethtypes.Log{}); ok {
		t.Fatal("expected !ok on empty logs")
	}

	// Matching log:
	var topic1 chain.TxHash
	copy(topic1[12:], orch[:])
	data := make([]byte, 32)
	big.NewInt(99).FillBytes(data)
	logs := []ethtypes.Log{{
		Topics: []chain.TxHash{bondingmanager.EventReward, topic1},
		Data:   data,
	}}
	got, ok := svc.ParseEarnings(logs)
	if !ok {
		t.Fatal("expected match")
	}
	if got.Cmp(big.NewInt(99)) != 0 {
		t.Fatalf("amount = %s; want 99", got)
	}
}

func TestSetEarnings(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	bm := &stubBondingManager{
		addr:       common.HexToAddress("0x000000000000000000000000000000000000FB01"),
		transcoder: bondingmanager.TranscoderInfo{Active: true, ActivationRound: 1, LastRewardRound: 0},
		pool:       []chain.Address{orch},
	}
	sub := newStubSubmitter()
	svc := mustNewSvc(t, bm, sub, newCache(t), orch)

	if _, err := svc.TryReward(context.Background(), chain.Round{Number: 100}); err != nil {
		t.Fatal(err)
	}
	svc.SetEarnings(100, big.NewInt(123))
	st := svc.Status()
	if st.LastEarnedWei == nil || st.LastEarnedWei.Cmp(big.NewInt(123)) != 0 {
		t.Fatalf("LastEarnedWei = %v; want 123", st.LastEarnedWei)
	}
	// SetEarnings on different round = no-op
	svc.SetEarnings(999, big.NewInt(456))
	if st2 := svc.Status(); st2.LastEarnedWei.Cmp(big.NewInt(123)) != 0 {
		t.Fatalf("post-mismatch LastEarnedWei = %v; want 123", st2.LastEarnedWei)
	}
}

func TestPurgeBeforeOnRound(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	bm := &stubBondingManager{
		addr:       common.HexToAddress("0x000000000000000000000000000000000000FB01"),
		transcoder: bondingmanager.TranscoderInfo{Active: true, ActivationRound: 1, LastRewardRound: 0},
		pool:       []chain.Address{orch},
	}
	sub := newStubSubmitter()
	cache := newCache(t)
	svc := mustNewSvc(t, bm, sub, cache, orch)

	// Pre-fill the cache with old entries.
	pcache := cache.(*poolhints.Cache)
	for r := chain.RoundNumber(50); r <= 60; r++ {
		_ = pcache.Put(r, orch, types.PoolHints{})
	}

	// Trigger a reward at round 100; purge window is 5 (default), so
	// rounds < 95 get purged.
	if _, err := svc.TryReward(context.Background(), chain.Round{Number: 100}); err != nil {
		t.Fatal(err)
	}
	count, err := pcache.CountForRound(50)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("round 50 not purged: count = %d", count)
	}
}

func TestStatusEmpty(t *testing.T) {
	orch := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	bm := &stubBondingManager{addr: common.HexToAddress("0x000000000000000000000000000000000000FB01")}
	sub := newStubSubmitter()
	svc := mustNewSvc(t, bm, sub, newCache(t), orch)
	st := svc.Status()
	if st.LastRound != 0 || st.LastError != "" || st.LastIntent != nil {
		t.Fatalf("empty status not empty: %+v", st)
	}
}

func TestRewardKey(t *testing.T) {
	addr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	k := rewardKey(42, addr)
	if len(k) != 28 {
		t.Fatalf("rewardKey len = %d; want 28", len(k))
	}
}

// stubRoundClock is the minimal SubscribeRounds-only RoundClock for tests.
type stubRoundClock struct {
	rounds chan chain.Round
	subErr error
}

func (s *stubRoundClock) SubscribeRounds(_ context.Context) (<-chan chain.Round, error) {
	if s.subErr != nil {
		return nil, s.subErr
	}
	return s.rounds, nil
}
func (s *stubRoundClock) SubscribeL1Blocks(_ context.Context) (<-chan chain.BlockNumber, error) {
	return nil, nil
}
func (s *stubRoundClock) Current(_ context.Context) (chain.Round, error) {
	return chain.Round{}, nil
}
