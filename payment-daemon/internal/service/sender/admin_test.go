package sender

import (
	"context"
	"testing"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devclock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A live conformance run needs to move the round to demonstrate the
// encumbrance release rule end to end. On a real chain that is hours per
// round, so without this the rule can only be reasoned about.
func TestAdvanceDevRound(t *testing.T) {
	c := devclock.New()
	before := c.LastInitializedRound()
	admin := NewAdmin(c)

	resp, err := admin.AdvanceDevRound(context.Background(), &pb.AdvanceDevRoundRequest{Rounds: 3})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetCurrentRound() != before+3 {
		t.Fatalf("round = %d; want %d", resp.GetCurrentRound(), before+3)
	}
	if c.LastInitializedRound() != before+3 {
		t.Fatalf("clock did not move: %d", c.LastInitializedRound())
	}
}

// Refused on a chain clock. A daemon that could fake rounds could make
// an expired envelope look live to anything reading its clock, which is
// exactly what the release rule depends on being honest.
func TestAdvanceDevRoundRefusedWithoutDevClock(t *testing.T) {
	_, err := NewAdmin(nil).AdvanceDevRound(context.Background(),
		&pb.AdvanceDevRoundRequest{Rounds: 1})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("err = %v; want FailedPrecondition on a non-dev clock", err)
	}
}

func TestAdvanceDevRoundRejectsNonPositive(t *testing.T) {
	admin := NewAdmin(devclock.New())
	for _, n := range []int64{0, -1} {
		if _, err := admin.AdvanceDevRound(context.Background(),
			&pb.AdvanceDevRoundRequest{Rounds: n}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("rounds=%d gave %v; want InvalidArgument", n, err)
		}
	}
}

// The clock never goes backwards. A consumer treats a regressing round
// as "stay encumbered", so a clock that could regress would strand
// encumbrances rather than release them early — but it should not
// regress in the first place.
func TestAdvanceRoundsIsMonotonic(t *testing.T) {
	c := devclock.New()
	last := c.LastInitializedRound()
	for i := 0; i < 5; i++ {
		got := c.AdvanceRounds(2)
		if got <= last {
			t.Fatalf("round went from %d to %d", last, got)
		}
		last = got
	}
}
