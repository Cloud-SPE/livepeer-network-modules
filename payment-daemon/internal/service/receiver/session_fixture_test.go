package receiver_test

import (
	"encoding/hex"
	"math/big"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/receiver"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
	"google.golang.org/grpc"
)

// Compiled by capability-broker/scripts/test-session-revisions.sh. Only fixture
// treasury setup uses store methods; the broker tests exercise the real service
// and Bolt ledger exclusively through gRPC, including process restarts.
func TestSessionReceiverFixture(t *testing.T) {
	dir := os.Getenv("SESSION_RECEIVER_FIXTURE_DIR")
	if dir == "" {
		t.Skip("subprocess fixture for broker integration tests")
	}
	payer, err := hex.DecodeString(os.Getenv("SESSION_RECEIVER_FIXTURE_PAYER"))
	if err != nil || len(payer) != 20 {
		t.Fatal("invalid fixture payer")
	}
	st, err := store.Open(filepath.Join(dir, "receiver.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	payee := bytes20(0xaa)
	account, err := st.GetWholesaleAccount(payer, payee)
	if err != nil {
		t.Fatal(err)
	}
	if account.CreditedWei == "0" {
		if _, _, err := st.OpenSession(store.Session{WorkID: "fixture-float", Capability: "custom:any", Offering: "offer", PricePerWorkUnitWei: "1", PerUnits: 1, WorkUnit: "units"}); err != nil {
			t.Fatal(err)
		}
		if err := st.SealSender("fixture-float", payer); err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreditBalance(payer, "fixture-float", big.NewInt(1_000_000_000_000_000)); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		seed := store.WholesaleAuthorizationSeed{ID: "fixture-bootstrap", Fingerprint: []byte("fixture"), Payer: payer, Payee: payee, MaxDebitWei: "1", MaxTotalUnits: 1, PriceWei: "1", PerUnits: 1, ExpiresAt: now.Add(time.Hour)}
		if _, err := st.AdmitWholesale(seed, "fixture-float", big.NewInt(1), now); err != nil {
			t.Fatal(err)
		}
		if _, err := st.SettleWholesale(payer, payee, seed.ID, 0, 1, now); err != nil {
			t.Fatal(err)
		}
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(stop)
	socket := filepath.Join(dir, "receiver.sock")
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	svc := receiver.New(st, receiver.Config{SettlementDomainID: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Recipient: payee}, nil)
	server := grpc.NewServer()
	pb.RegisterPayeeDaemonServer(server, svc)
	go func() { _ = server.Serve(listener) }()
	<-stop
	server.GracefulStop()
}
