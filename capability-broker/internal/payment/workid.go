package payment

import (
	"encoding/hex"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

// DerivePayeeWorkID recovers the payee-side session key from the inbound
// payment bytes. Quote-free tickets are keyed by the recipient_rand_hash
// the payee issued with its TicketParams, and the payee daemon binds its
// session to that same value — so the broker MUST reuse it as work_id or
// OpenSession mints a session whose recipient rand the sender never saw,
// and every ticket in the payment fails validation against it.
//
// Workload admission no longer uses this identity. It remains relevant only
// while validating a funding ticket generation before moving credited value
// into the stable payer-payee account.
//
// Returns ("", false) for malformed or opaque payment blobs that carry no
// ticket params.
func DerivePayeeWorkID(paymentBytes []byte) (string, bool) {
	var pay pb.Payment
	if err := proto.Unmarshal(paymentBytes, &pay); err != nil {
		return "", false
	}
	tp := pay.GetTicketParams()
	if tp == nil || len(tp.GetRecipientRandHash()) == 0 {
		return "", false
	}
	return hex.EncodeToString(tp.GetRecipientRandHash()), true
}
