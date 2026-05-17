package middleware

import (
	"encoding/hex"
	"strconv"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

// DerivePayeeWorkID attempts to recover the payee-side session key from
// the inbound payment bytes. Quote-free tickets are keyed by the
// recipient_rand_hash issued by the payee; the broker reuses that as
// work_id so OpenSession/ProcessPayment/DebitBalance/CloseSession all
// bind to the same receiver-side session. Returns ("", false) for
// legacy mock/stub payment blobs.
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

// DerivePaymentRoundID extracts the payment creation round from the wire
// message when present. Returns "" for legacy stub bytes or missing
// expiration params.
func DerivePaymentRoundID(paymentBytes []byte) string {
	var pay pb.Payment
	if err := proto.Unmarshal(paymentBytes, &pay); err != nil {
		return ""
	}
	exp := pay.GetExpirationParams()
	if exp == nil || exp.GetCreationRound() <= 0 {
		return ""
	}
	return strconv.FormatInt(exp.GetCreationRound(), 10)
}
