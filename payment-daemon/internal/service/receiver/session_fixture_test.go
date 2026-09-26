package receiver_test

import (
	"crypto/sha256"
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

	for i, accountID := range []string{"test-account", "other-account"} {
		account, err := st.GetWholesaleAccount(payer, payee, accountID)
		if err != nil {
			t.Fatal(err)
		}
		if account.CreditedWei == "0" {
			work := "fixture-float-" + accountID
			_, _, err = st.GetOrCreateTicketSession(store.TicketSessionKey{Sender: payer, Recipient: payee, Capability: "c", Offering: "o", WholesaleAccountID: accountID, TicketStreamID: "fixture"}, store.Session{WorkID: work, RecipientRand: big.NewInt(int64(i + 1)).String()})
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256([]byte(accountID))
			if _, _, err = st.ApplyWholesaleFunding(payer, payee, accountID, work, hex.EncodeToString(digest[:]), []store.FundingTicket{{Nonce: 1, Credit: big.NewInt(1_000_000_000_000_000)}}, time.Now()); err != nil {
				t.Fatal(err)
			}
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
	svc := receiver.New(st, receiver.Config{ChainID: 42161, SettlementDomainID: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Recipient: payee}, nil)
	server := grpc.NewServer()
	pb.RegisterPayeeDaemonServer(server, svc)
	go func() { _ = server.Serve(listener) }()
	<-stop
	server.GracefulStop()
}
