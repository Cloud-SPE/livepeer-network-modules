package workledger

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

type deviceReceiver struct {
	payment.AccountClient
	state      int32
	admissions int
}

func (r *deviceReceiver) GetSpendAuthorization(context.Context, []byte, string) (*payment.SpendAuthorizationStatus, error) {
	return &payment.SpendAuthorizationStatus{State: r.state}, nil
}
func (r *deviceReceiver) AdmitAuthorization(context.Context, payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	r.admissions++
	return &payment.AdmitAuthorizationResult{State: r.state}, nil
}
func deviceAuth(t *testing.T, id, previous string) []byte {
	t.Helper()
	raw, err := proto.Marshal(&pb.SpendAuthorization{Payload: &pb.SpendAuthorizationPayload{AuthorizationId: id, PredecessorAuthorizationId: previous, SettlementDomainId: "source", Payer: make([]byte, 20)}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestDeviceDrainFencesNewBindingAndRenewalAcrossRestart(t *testing.T) {
	s, path := testStore(t)
	ctx := context.Background()
	receiver := &deviceReceiver{state: int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED)}
	client := &Client{Store: s, AccountClient: receiver}
	a := Attribution{Member: "member", Enrollment: "host", Backend: "host|gpu", Capability: "cap", Offering: "offer", DeviceOwnership: map[string]uint64{"gpu-a": 1}}
	raw := deviceAuth(t, "job", "")
	if err := s.SaveAuthorization("job", raw); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind("job", a); err != nil {
		t.Fatal(err)
	}
	if err := client.BeginDeviceDrain("host", "GPU-A", 1, "regional transfer"); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind("new", a); err == nil {
		t.Fatal("stale selected backend bound after drain")
	}
	if _, err := client.AdmitAuthorization(ctx, payment.AdmitAuthorizationRequest{AuthorizationBytes: deviceAuth(t, "renewal", "job")}); !errors.Is(err, payment.ErrAdmissionStopped) {
		t.Fatalf("renewal escaped drain: %v", err)
	}
	if receiver.admissions != 0 {
		t.Fatal("renewal reached receiver")
	}
	if _, err := client.AdmitAuthorization(ctx, payment.AdmitAuthorizationRequest{AuthorizationBytes: raw}); err != nil {
		t.Fatal(err)
	}
	sibling := a
	sibling.DeviceOwnership = map[string]uint64{"gpu-b": 1}
	if err := s.Bind("sibling", sibling); err != nil {
		t.Fatal("sibling blocked", err)
	}
	proof, err := client.DeviceDrainStatus(ctx, "host", "gpu-a", 1)
	if err != nil || proof.ActiveAuthorizations != 1 {
		t.Fatalf("active proof %+v %v", proof, err)
	}
	op, err := s.Prepare(Operation{AuthorizationID: "job", Sequence: 1, Kind: "settle", Units: 2})
	if err != nil {
		t.Fatal(err)
	}
	receiver.state = int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED)
	proof, err = client.DeviceDrainStatus(ctx, "host", "gpu-a", 1)
	if err != nil || proof.PendingOperations != 1 {
		t.Fatalf("pending operation %+v %v", proof, err)
	}
	if err := s.Complete(op.ID, big.NewInt(7)); err != nil {
		t.Fatal(err)
	}
	if err := s.Finalize(100, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, "pool", "source", "broker")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	client.Store = s
	if err := s.Bind("late", a); err == nil {
		t.Fatal("restart lost drain")
	}
	proof, err = client.DeviceDrainStatus(ctx, "host", "gpu-a", 1)
	if err != nil || proof.UndeliveredReceipts != 1 || proof.ActiveAuthorizations != 0 {
		t.Fatalf("receipt coverage %+v %v", proof, err)
	}
	queue, _ := s.Undelivered(0)
	if err := s.Delivered(queue[0].ID); err != nil {
		t.Fatal(err)
	}
	proof, err = client.DeviceDrainStatus(ctx, "host", "gpu-a", 1)
	if err != nil || proof.UndeliveredReceipts+proof.PendingOperations+proof.ActiveAuthorizations+proof.UnqualifiedWork != 0 {
		t.Fatalf("quiet proof %+v %v", proof, err)
	}
	// A future exclusive assignment generation is independent of the old fence.
	a.DeviceOwnership = map[string]uint64{"gpu-a": 2}
	if err := s.Bind("future-generation", a); err != nil {
		t.Fatal(err)
	}
}

func TestRegionalDeviceDrainConformance(t *testing.T) {
	raw, err := os.ReadFile("../../../livepeer-network-protocol/conformance/fixtures/regional-device-drain.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		EnrollmentID string `json:"enrollment_id"`
		Drain        struct {
			DeviceID   string `json:"device_id"`
			Generation uint64 `json:"generation"`
		} `json:"drain"`
		NewWork []struct {
			ID         string `json:"id"`
			DeviceID   string `json:"device_id"`
			Generation uint64 `json:"generation"`
			Allowed    bool   `json:"allowed"`
		} `json:"new_work"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	s, _ := testStore(t)
	defer s.Close()
	c := &Client{Store: s}
	if err := c.BeginDeviceDrain(fixture.EnrollmentID, fixture.Drain.DeviceID, fixture.Drain.Generation, "conformance"); err != nil {
		t.Fatal(err)
	}
	for _, item := range fixture.NewWork {
		err := s.Bind(item.ID, Attribution{Member: "member", Enrollment: fixture.EnrollmentID, Backend: "host|gpu", Capability: "cap", Offering: "offer", DeviceOwnership: map[string]uint64{item.DeviceID: item.Generation}})
		if (err == nil) != item.Allowed {
			t.Fatalf("%s allowed=%v err=%v", item.ID, item.Allowed, err)
		}
	}
}
