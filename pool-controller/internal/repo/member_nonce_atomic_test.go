package repo

import (
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"sync"
	"testing"
	"time"
)

func TestMemberNonceConsumptionHasOneWinner(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	if err := st.PutMemberNonce(types.MemberNonce{ID: "nonce", ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- st.MarkMemberNonceUsed("nonce", now) }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("nonce consumed %d times", successes)
	}
}
