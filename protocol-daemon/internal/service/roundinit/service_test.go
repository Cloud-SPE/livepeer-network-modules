package roundinit

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/txintent"
)

// stubRoundsManager is a roundinit.RoundsManager that returns canned values.
type stubRoundsManager struct {
	addr        chain.Address
	initialized bool
	initErr     error
	packErr     error
	calls       int
	mu          sync.Mutex
}

func (s *stubRoundsManager) Address() chain.Address { return s.addr }
func (s *stubRoundsManager) CurrentRoundInitialized(_ context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.initialized, s.initErr
}
func (s *stubRoundsManager) PackInitializeRound() ([]byte, error) {
	if s.packErr != nil {
		return nil, s.packErr
	}
	return []byte{0x01, 0x02, 0x03, 0x04}, nil
}

// stubSubmitter is a TxSubmitter that records the params and returns a
// deterministic IntentID. Idempotency mimicked: same KeyParams returns
// the same IntentID without recording a second submission.
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
	key := id.Hex()
	if existing, ok := s.idMap[key]; ok {
		return existing, nil
	}
	s.idMap[key] = id
	s.submitted = append(s.submitted, p)
	return id, nil
}

func (s *stubSubmitter) Status(_ context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
	return txintent.TxIntent{ID: id}, nil
}

func (s *stubSubmitter) Wait(_ context.Context, id txintent.IntentID) (txintent.TxIntent, error) {
	return txintent.TxIntent{ID: id, Status: txintent.StatusConfirmed}, nil
}

func newSvc(t *testing.T, rm *stubRoundsManager, sub *stubSubmitter, jitter time.Duration) *Service {
	t.Helper()
	svc, err := New(Config{
		RoundsManager: rm,
		TxIntent:      sub,
		GasLimit:      1_000_000,
		InitJitter:    jitter,
		Rand:          rand.New(rand.NewSource(1)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestNewValidates(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error on empty config")
	}
	if _, err := New(Config{RoundsManager: &stubRoundsManager{}}); err == nil {
		t.Fatal("expected error on missing TxIntent")
	}
	if _, err := New(Config{RoundsManager: &stubRoundsManager{}, TxIntent: newStubSubmitter()}); err == nil {
		t.Fatal("expected error on missing GasLimit")
	}
}

func TestTryInitializeAlreadyInitialized(t *testing.T) {
	rm := &stubRoundsManager{addr: common.HexToAddress("0x000000000000000000000000000000000000FA01"), initialized: true}
	sub := newStubSubmitter()
	svc := newSvc(t, rm, sub, 0)

	res, err := svc.TryInitialize(context.Background(), chain.Round{Number: 100})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skip == nil {
		t.Fatal("expected typed Skip for already-initialized round")
	}
	if res.Skip.Code != SkipCodeRoundInitialized {
		t.Fatalf("Skip.Code = %d; want SkipCodeRoundInitialized (%d)", res.Skip.Code, SkipCodeRoundInitialized)
	}
	if res.Skip.Reason != "round already initialized" {
		t.Fatalf("Skip.Reason = %q", res.Skip.Reason)
	}
	if res.IntentID != (txintent.IntentID{}) {
		t.Fatal("TryInitialize returned non-zero ID for already-initialized round")
	}
	if len(sub.submitted) != 0 {
		t.Fatal("submit should not be called for already-initialized round")
	}
	st := svc.Status()
	if !st.CurrentInitialized {
		t.Fatal("Status.CurrentInitialized = false; want true after observing already-initialized round")
	}
	if st.LastIntent != nil {
		t.Fatalf("Status.LastIntent = %v; want nil after skipped path", st.LastIntent)
	}
}

func TestTryInitializeSubmits(t *testing.T) {
	rm := &stubRoundsManager{addr: common.HexToAddress("0x000000000000000000000000000000000000FA01")}
	sub := newStubSubmitter()
	svc := newSvc(t, rm, sub, 0)

	res, err := svc.TryInitialize(context.Background(), chain.Round{Number: 100})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skip != nil {
		t.Fatalf("expected submit, got skip: %+v", res.Skip)
	}
	if res.IntentID == (txintent.IntentID{}) {
		t.Fatal("TryInitialize returned zero ID")
	}
	if len(sub.submitted) != 1 {
		t.Fatalf("submitted len = %d; want 1", len(sub.submitted))
	}
	if sub.submitted[0].Kind != "InitializeRound" {
		t.Fatalf("kind = %s; want InitializeRound", sub.submitted[0].Kind)
	}
	if got := chain.RoundNumber(decodeKey(sub.submitted[0].KeyParams)); got != 100 {
		t.Fatalf("KeyParams round = %d; want 100", got)
	}
	if sub.submitted[0].To != rm.addr {
		t.Fatalf("To = %s; want %s", sub.submitted[0].To.Hex(), rm.addr.Hex())
	}
	if sub.submitted[0].GasLimit == 0 {
		t.Fatal("GasLimit not propagated")
	}
	// Just-submitted: on-chain state was uninitialized when we queried,
	// so CurrentInitialized stays false until the next tick re-queries.
	// LastIntent points at the in-flight tx.
	st := svc.Status()
	if st.CurrentInitialized {
		t.Fatal("Status.CurrentInitialized = true; want false right after submit (tx not mined yet)")
	}
	if st.LastIntent == nil || *st.LastIntent != res.IntentID {
		t.Fatalf("Status.LastIntent = %v; want %s", st.LastIntent, res.IntentID.Hex())
	}
}

func TestTryInitializeIdempotent(t *testing.T) {
	rm := &stubRoundsManager{addr: common.HexToAddress("0x000000000000000000000000000000000000FA01")}
	sub := newStubSubmitter()
	svc := newSvc(t, rm, sub, 0)

	res1, err := svc.TryInitialize(context.Background(), chain.Round{Number: 100})
	if err != nil {
		t.Fatal(err)
	}
	res2, err := svc.TryInitialize(context.Background(), chain.Round{Number: 100})
	if err != nil {
		t.Fatal(err)
	}
	if res1.IntentID != res2.IntentID {
		t.Fatalf("idempotent submit returned different IDs: %s vs %s", res1.IntentID.Hex(), res2.IntentID.Hex())
	}
	if len(sub.submitted) != 1 {
		t.Fatalf("idempotent submit should not record a second call: got %d", len(sub.submitted))
	}
}

func TestTryInitializeReadError(t *testing.T) {
	rm := &stubRoundsManager{
		addr:    common.HexToAddress("0x000000000000000000000000000000000000FA01"),
		initErr: errors.New("rpc down"),
	}
	sub := newStubSubmitter()
	svc := newSvc(t, rm, sub, 0)

	if _, err := svc.TryInitialize(context.Background(), chain.Round{Number: 100}); err == nil {
		t.Fatal("expected error")
	}
	if len(sub.submitted) != 0 {
		t.Fatal("submit should not be called when read failed")
	}
}

func TestTryInitializePackError(t *testing.T) {
	rm := &stubRoundsManager{
		addr:    common.HexToAddress("0x000000000000000000000000000000000000FA01"),
		packErr: errors.New("pack failed"),
	}
	sub := newStubSubmitter()
	svc := newSvc(t, rm, sub, 0)
	if _, err := svc.TryInitialize(context.Background(), chain.Round{Number: 100}); err == nil {
		t.Fatal("expected error")
	}
}

func TestTryInitializeSubmitError(t *testing.T) {
	rm := &stubRoundsManager{addr: common.HexToAddress("0x000000000000000000000000000000000000FA01")}
	sub := newStubSubmitter()
	sub.failNext = errors.New("submit failed")
	svc := newSvc(t, rm, sub, 0)

	if _, err := svc.TryInitialize(context.Background(), chain.Round{Number: 100}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunStopsOnContext(t *testing.T) {
	rm := &stubRoundsManager{addr: common.HexToAddress("0x000000000000000000000000000000000000FA01")}
	sub := newStubSubmitter()
	svc := newSvc(t, rm, sub, 0)

	rc := &stubRoundClock{rounds: make(chan chain.Round, 1)}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx, rc) }()

	rc.rounds <- chain.Round{Number: 50}
	// give Run a moment to process
	time.Sleep(20 * time.Millisecond)
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v; want context.Canceled", err)
	}
	st := svc.Status()
	if st.LastRound != 50 {
		t.Fatalf("Status.LastRound = %d; want 50", st.LastRound)
	}
}

func TestRunRecordsErrorOutcome(t *testing.T) {
	rm := &stubRoundsManager{
		addr:    common.HexToAddress("0x000000000000000000000000000000000000FA01"),
		initErr: errors.New("rpc down"),
	}
	sub := newStubSubmitter()
	svc := newSvc(t, rm, sub, 0)

	rc := &stubRoundClock{rounds: make(chan chain.Round, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx, rc) }()

	rc.rounds <- chain.Round{Number: 7}
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	st := svc.Status()
	if st.LastError == "" {
		t.Fatal("expected LastError to be set")
	}
}

func TestRunSubscribeError(t *testing.T) {
	rm := &stubRoundsManager{addr: common.HexToAddress("0x000000000000000000000000000000000000FA01")}
	sub := newStubSubmitter()
	svc := newSvc(t, rm, sub, 0)
	rc := &stubRoundClock{subErr: errors.New("subscribe failed")}
	if err := svc.Run(context.Background(), rc); err == nil {
		t.Fatal("expected subscribe error")
	}
}

func TestRunStopsWhenChannelClosed(t *testing.T) {
	rm := &stubRoundsManager{addr: common.HexToAddress("0x000000000000000000000000000000000000FA01"), initialized: true}
	sub := newStubSubmitter()
	svc := newSvc(t, rm, sub, 0)

	rc := &stubRoundClock{rounds: make(chan chain.Round, 1)}
	rc.rounds <- chain.Round{Number: 10}
	close(rc.rounds)

	if err := svc.Run(context.Background(), rc); err != nil {
		t.Fatalf("Run err = %v; want nil on closed channel", err)
	}
}

func TestStatusEmpty(t *testing.T) {
	rm := &stubRoundsManager{addr: common.HexToAddress("0x000000000000000000000000000000000000FA01")}
	sub := newStubSubmitter()
	svc := newSvc(t, rm, sub, 0)
	st := svc.Status()
	if st.LastRound != 0 {
		t.Fatalf("empty status LastRound = %d; want 0", st.LastRound)
	}
	if st.LastIntent != nil {
		t.Fatal("empty status LastIntent should be nil")
	}
	if st.LastError != "" {
		t.Fatalf("empty status LastError = %q; want empty", st.LastError)
	}
}

func TestJitterNonzero(t *testing.T) {
	rm := &stubRoundsManager{addr: common.HexToAddress("0x000000000000000000000000000000000000FA01")}
	sub := newStubSubmitter()
	svc := newSvc(t, rm, sub, 50*time.Millisecond)

	start := time.Now()
	if _, err := svc.TryInitialize(context.Background(), chain.Round{Number: 100}); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	// Jitter is bounded by 50ms; with seed=1 the deterministic result is
	// nonzero. Just assert the service can complete with jitter.
	if elapsed > 200*time.Millisecond {
		t.Fatalf("jitter too large: %v", elapsed)
	}
}

func TestJitterContextCancel(t *testing.T) {
	rm := &stubRoundsManager{addr: common.HexToAddress("0x000000000000000000000000000000000000FA01")}
	sub := newStubSubmitter()
	svc := newSvc(t, rm, sub, 1*time.Hour) // huge jitter; ctx cancel will short-circuit

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if _, err := svc.TryInitialize(ctx, chain.Round{Number: 100}); err == nil {
		t.Fatal("expected ctx cancel error")
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

// decodeKey reads back a chain.RoundNumber from a Bytes()-encoded round number.
func decodeKey(b []byte) uint64 {
	if len(b) != 8 {
		return 0
	}
	var v uint64
	for _, x := range b {
		v = (v << 8) | uint64(x)
	}
	return v
}
