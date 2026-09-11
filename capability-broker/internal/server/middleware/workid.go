package middleware

import (
	"strconv"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

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
