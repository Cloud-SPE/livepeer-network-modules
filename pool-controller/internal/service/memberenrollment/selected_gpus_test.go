package memberenrollment

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestSelectedGPUsPersistThroughRotationAndBundle(t *testing.T) {
	st, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	wallet := "0x1111111111111111111111111111111111111111"
	if err := st.PutPoolMember(types.PoolMember{ID: wallet, EthAddress: wallet}); err != nil {
		t.Fatal(err)
	}
	svc := New(st)
	created, err := svc.CreateEnrollment(CreateEnrollmentRequest{MemberEthAddress: wallet, GPUUUIDs: []string{"GPU-B", " gpu-a "}})
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"gpu-a", "gpu-b"}
	if !reflect.DeepEqual(created.Enrollment.GPUUUIDs, expected) {
		t.Fatal(created.Enrollment.GPUUUIDs)
	}
	changed := created.Enrollment
	changed.GPUUUIDs = []string{"gpu-c"}
	if err := st.PutHostEnrollment(changed); err != nil {
		t.Fatal(err)
	}
	rotated, _, err := svc.Rotate(created.Enrollment.ID)
	if err != nil || !reflect.DeepEqual(rotated.GPUUUIDs, expected) {
		t.Fatalf("selection changed %+v %v", rotated, err)
	}
	env := bundleEnv(BundleInput{Enrollment: rotated})
	if !strings.Contains(env, "POOL_GPU_UUIDS=gpu-a,gpu-b\n") {
		t.Fatal("bundle lost selection", env)
	}
	if !strings.Contains(env, "LIVEPEER_EDGE_PORT=0\n") || !strings.Contains(env, "LIVEPEER_EDGE_RTMPS_PORT=0\n") {
		t.Fatal("selected bundle would collide with retained source agent ports")
	}
	for _, selection := range [][]string{{"gpu-a", "GPU-A"}, {""}, {"gpu-a\nOTHER=1"}, {"../gpu"}} {
		if _, err := svc.CreateEnrollment(CreateEnrollmentRequest{MemberEthAddress: wallet, GPUUUIDs: selection}); err == nil {
			t.Fatalf("unsafe selection %q accepted", selection)
		}
	}
	all, err := svc.CreateEnrollment(CreateEnrollmentRequest{MemberEthAddress: wallet})
	if err != nil || len(all.Enrollment.GPUUUIDs) != 0 {
		t.Fatal("default discovery changed", err)
	}
}
