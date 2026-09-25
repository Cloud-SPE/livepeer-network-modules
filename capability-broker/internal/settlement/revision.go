package settlement

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func EncodeRevision(rec *pb.SessionRevisionRecord, signer *Signer) (string, error) {
	if signer == nil || rec == nil || rec.GetEvidenceDomain() != "livepeer-session-revision/v1" {
		return "", fmt.Errorf("signed revision evidence unavailable")
	}
	if rec.GetOutcome() != pb.SessionRevisionRecord_ADMITTED && (rec.GetOutcome() != pb.SessionRevisionRecord_NOT_ADMITTED || (rec.GetNonAdmissionBasis() != "receiver_fenced" && rec.GetNonAdmissionBasis() != "expired_unused")) {
		return "", fmt.Errorf("revision proof missing")
	}
	raw, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(rec)
	if err != nil {
		return "", err
	}
	canonical, err := canonicalize(raw)
	if err != nil {
		return "", err
	}
	value, err := signer.Sign(canonical)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(Envelope{Payload: canonical, Signature: &Signature{Algorithm: Alg, Canonicalization: Canonicalization, Value: value}})
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out), nil
}
