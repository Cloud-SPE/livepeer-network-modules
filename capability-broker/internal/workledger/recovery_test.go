package workledger

import (
	"context"
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"
)

type orphanReceiver struct {
	payment.AccountClient
	calls []string
	fail  bool
}

func (r *orphanReceiver) CloseUnexecutedAuthorization(_ context.Context, _ []byte, id, reason string) error {
	r.calls = append(r.calls, id)
	if r.fail {
		return fmt.Errorf("recovery unavailable")
	}
	if reason == "" {
		return fmt.Errorf("missing reason")
	}
	return nil
}
func TestRestartRecoveryOnlyFencesUnboundAdmissions(t *testing.T) {
	s, path := testStore(t)
	for _, id := range []string{"unbound", "bound"} {
		raw, _ := proto.Marshal(&pb.SpendAuthorization{Payload: &pb.SpendAuthorizationPayload{AuthorizationId: id, SettlementDomainId: "source", Payer: make([]byte, 20)}})
		if err := s.SaveAuthorization(id, raw); err != nil {
			t.Fatal(err)
		}
	}
	bind(t, s, "bound")
	if err := s.Finalize(101, time.Now()); err != nil {
		t.Fatal(err)
	}
	if proof, _ := s.Report(100, time.Now()); proof.Complete {
		t.Fatal("unbound admission became zero work")
	}
	s.Close()
	var err error
	s, err = Open(path, "pool", "source", "broker")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	receiver := &orphanReceiver{fail: true}
	client := &Client{Store: s, AccountClient: receiver}
	if err = client.RecoverUnbound(context.Background()); err == nil {
		t.Fatal("uncertain orphan recovery accepted")
	}
	state, _ := s.DrainStatus()
	if state.PendingOperations != 1 {
		t.Fatal("failed recovery lost durable admission")
	}
	receiver.fail = false
	if err = client.RecoverUnbound(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = client.RecoverUnbound(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(receiver.calls) != 2 || receiver.calls[0] != "unbound" || receiver.calls[1] != "unbound" {
		t.Fatalf("bound work touched or resolved work reprocessed: %v", receiver.calls)
	}
	if proof, _ := s.Report(100, time.Now()); !proof.Complete {
		t.Fatalf("recovered zero work still held: %+v", proof)
	}
}
