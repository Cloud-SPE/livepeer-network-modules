package repo

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestAtomicRotationPreservesGrantsAndRejectsStaleSecretRestore(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	initial := types.HostEnrollment{ID: "host", MemberEthAddress: "0x1111111111111111111111111111111111111111", Status: types.HostEnrollmentActive, EnrollmentTokenHash: "old-hash", BrokerSessionCredential: "old-secret", DeviceOwnership: map[string]uint64{"gpu-b": 2}}
	if err := st.PutHostEnrollment(initial); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := st.RotateEnrollmentCredentials("host", "old-hash", fmt.Sprintf("hash-%d", i), fmt.Sprintf("secret-%d", i), time.Now())
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("rotation winners %d", winners)
	}
	rotated, err := st.GetHostEnrollment("host")
	if err != nil {
		t.Fatal(err)
	}
	initial.LastSeenAt = time.Now()
	if err := st.PutHostEnrollment(initial); err != nil {
		t.Fatal(err)
	}
	after, err := st.GetHostEnrollment("host")
	if err != nil {
		t.Fatal(err)
	}
	if after.EnrollmentTokenHash != rotated.EnrollmentTokenHash || after.BrokerSessionCredential != rotated.BrokerSessionCredential || after.DeviceOwnership["gpu-b"] != 2 {
		t.Fatal("stale heartbeat restored secrets or lost ownership")
	}
	if _, err := st.RevokeHostEnrollment("host", "retired", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RotateEnrollmentCredentials("host", rotated.EnrollmentTokenHash, "new", "new", time.Now()); err == nil {
		t.Fatal("revoked host rotated")
	}
}
