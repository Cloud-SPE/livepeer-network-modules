package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/receipts"
	paymentsv1 "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// CapabilitySpec is the price curve an authorization must bind exactly.
type CapabilitySpec struct {
	WorkUnit            string
	PricePerWorkUnitWei *big.Int
	// PerUnits is the denominator the price is quoted over
	// (offering-axes.md §6). 0 means 1.
	PerUnits uint64
}

// CapabilityLookup resolves a (capability_id, offering_id) pair to its
// pricing metadata. The broker wires `s.lookup` into this; the
// middleware uses it without depending on internal/config.
type CapabilityLookup func(capability, offering string) (CapabilitySpec, bool)

// SessionState is the per-request handle the dispatch layer uses to
// publish a LiveCounter back to the payment middleware. The middleware
// creates one before invoking next.ServeHTTP and stuffs it into the
// request context; the dispatch layer reads it before invoking the
// driver and calls SetLiveCounter once the driver's Params are ready.
//
// SessionState is intentionally goroutine-safe: the ticker reads
// LiveCounter() concurrently with dispatch's SetLiveCounter call.
type SessionState struct {
	live atomic.Pointer[liveCounterHolder]
	meta atomic.Pointer[receiptMetaHolder]
}

type liveCounterHolder struct {
	lc extractors.LiveCounter
}

type receiptMetaHolder struct {
	meta ReceiptMeta
}

type ReceiptMeta struct {
	WorkID           string
	RoundID          string
	RequestID        string
	CapabilityID     string
	OfferingID       string
	MemberEthAddress string
	BackendID        string
	HostEnrollmentID string
	HardwareUnitID   string
	GPUUUID          string
	TemplateID       string
	ExpectedMaxUnits uint64
}

// SetLiveCounter publishes a LiveCounter for the in-flight session.
// Called by the dispatch layer once the driver's Params are constructed.
// nil is a no-op (mode driver does not support interim debit).
func (s *SessionState) SetLiveCounter(lc extractors.LiveCounter) {
	if s == nil || lc == nil {
		return
	}
	s.live.Store(&liveCounterHolder{lc: lc})
}

// LiveCounter returns the current LiveCounter or nil. Safe for
// concurrent use.
func (s *SessionState) LiveCounter() extractors.LiveCounter {
	if s == nil {
		return nil
	}
	if h := s.live.Load(); h != nil {
		return h.lc
	}
	return nil
}

func (s *SessionState) SetReceiptMeta(meta ReceiptMeta) {
	if s == nil {
		return
	}
	s.meta.Store(&receiptMetaHolder{meta: meta})
}

func (s *SessionState) ReceiptMeta() (ReceiptMeta, bool) {
	if s == nil {
		return ReceiptMeta{}, false
	}
	if h := s.meta.Load(); h != nil {
		return h.meta, true
	}
	return ReceiptMeta{}, false
}

type sessionStateKey struct{}

// SessionStateFromContext returns the SessionState attached by the
// payment middleware, or nil if absent (e.g. unpaid routes).
func SessionStateFromContext(ctx context.Context) *SessionState {
	if v := ctx.Value(sessionStateKey{}); v != nil {
		if s, ok := v.(*SessionState); ok {
			return s
		}
	}
	return nil
}

// Payment enforces authorization-backed account admission around one job.
// An optional payment credits account shortfall inside AdmitAuthorization;
// payment bytes never provide workload authority.
//
// SettlementEncoder renders a settlement record for the wire. Injected
// so the middleware does not reach for the server's signing key, and so
// a test can assert on the record without a key at all.
type SettlementEncoder func(*paymentsv1.SettlementRecord) (string, error)

func encodeSettlementRecord(record *paymentsv1.SettlementRecord) (string, error) {
	if record == nil {
		return "", errors.New("settlement record is nil")
	}
	raw, err := proto.Marshal(record)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func Payment(client payment.Client, lookup CapabilityLookup, receiptSink receipts.Client, encode SettlementEncoder, brokerURI ...string) Middleware {
	if encode == nil {
		encode = encodeSettlementRecord
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(livepeerheader.Authorization) == "" {
				livepeerheader.WriteError(w, http.StatusUnauthorized,
					livepeerheader.ErrAuthorizationRequired,
					"missing required header: "+livepeerheader.Authorization)
				return
			}
			uri := ""
			if len(brokerURI) > 0 {
				uri = brokerURI[0]
			}
			handleAccountAuthorizedJob(w, r, next, client, lookup, receiptSink, encode, uri)
			return
		})
	}
}

// handleAccountAuthorizedJob is the paid-job path. Admission
// reserves the full payer-authorized maximum atomically; settlement debits the
// measured units and returns the remainder to the stable payer-payee account.
func handleAccountAuthorizedJob(w http.ResponseWriter, r *http.Request, next http.Handler, client payment.Client, lookup CapabilityLookup, receiptSink receipts.Client, encode SettlementEncoder, brokerURI string) {
	account, ok := client.(payment.AccountClient)
	if !ok {
		livepeerheader.WriteError(w, http.StatusHTTPVersionNotSupported, livepeerheader.ErrProtocolUnsupported, "payment daemon does not support wholesale account authorizations")
		return
	}
	authBytes, err := base64.StdEncoding.DecodeString(r.Header.Get(livepeerheader.Authorization))
	if err != nil {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid, "Livepeer-Authorization is not valid base64: "+err.Error())
		return
	}
	var auth paymentsv1.SpendAuthorization
	if err := proto.Unmarshal(authBytes, &auth); err != nil || auth.GetPayload() == nil {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid, "Livepeer-Authorization is malformed")
		return
	}
	p := auth.GetPayload()
	capability, offering, protocol, requestID := r.Header.Get(livepeerheader.Capability), r.Header.Get(livepeerheader.Offering), r.Header.Get(livepeerheader.Protocol), RequestIDFromContext(r.Context())
	spec, found := lookup(capability, offering)
	if !found {
		livepeerheader.WriteError(w, http.StatusNotFound, livepeerheader.ErrCapabilityNotServed, "capability "+capability+"/"+offering+" is not served by this broker")
		return
	}
	if p.GetDomain() != "livepeer-spend-authorization/v1" || p.GetAuthorizationId() == "" || p.GetSessionId() != "" || p.GetRevision() != 0 || p.GetPredecessorAuthorizationId() != "" || p.GetRequestId() != requestID || p.GetProtocol() != protocol || protocol != "paid-job/v1" || p.GetCapability() != capability || p.GetOffering() != offering {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentEnvelopeMismatch, "authorization identity or route does not match this job")
		return
	}
	if p.GetChainId() == 0 || p.GetDenomination() != "wei" {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentEnvelopeMismatch, "authorization chain or denomination is invalid")
		return
	}
	if brokerURI == "" || strings.TrimRight(p.GetBrokerUri(), "/") != strings.TrimRight(brokerURI, "/") {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentEnvelopeMismatch, "authorization broker_uri does not match this broker")
		return
	}
	price := p.GetAcceptedPrice()
	wantPer := spec.PerUnits
	if wantPer == 0 {
		wantPer = 1
	}
	if price == nil || price.GetCapability() != capability || price.GetOffering() != offering || price.GetWorkUnitName() != spec.WorkUnit || price.GetUnitsPerPrice() != wantPer || spec.PricePerWorkUnitWei == nil || new(big.Int).SetBytes(price.GetPricePerUnitWei().GetValue()).Cmp(spec.PricePerWorkUnitWei) != 0 {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentEnvelopeMismatch, "authorization price does not match the broker offer")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (64<<20)+1))
	if err != nil || len(body) > 64<<20 {
		livepeerheader.WriteBadRequest(w, "request body cannot be committed")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	digest := sha256.Sum256(body)
	if len(p.GetRequestDigest()) != sha256.Size || !bytes.Equal(p.GetRequestDigest(), digest[:]) {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentEnvelopeMismatch, "authorization request_digest does not match the exact request body")
		return
	}
	if err := ValidateCallerProof(authBytes, p.GetCallerPublicKey(), r.Header.Get(livepeerheader.CallerProof)); err != nil {
		livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid, err.Error())
		return
	}
	var topup []byte
	if h := r.Header.Get(livepeerheader.Payment); h != "" {
		topup, err = base64.StdEncoding.DecodeString(h)
		if err != nil {
			livepeerheader.WriteError(w, http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid, "Livepeer-Payment is not valid base64: "+err.Error())
			return
		}
	}
	admitted, err := account.AdmitAuthorization(r.Context(), payment.AdmitAuthorizationRequest{AuthorizationBytes: authBytes, PaymentBytes: topup, Reservation: new(big.Int).SetBytes(p.GetMaxDebitWei().GetValue())})
	if err != nil {
		code, errCode := mapClientErr(err)
		if status.Code(err) == codes.FailedPrecondition {
			code, errCode = http.StatusPaymentRequired, livepeerheader.ErrInsufficientBalance
		}
		livepeerheader.WriteError(w, code, errCode, "admit authorization: "+err.Error())
		return
	}
	if admitted == nil || admitted.State != int32(paymentsv1.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) || admitted.Account == nil || !bytes.Equal(admitted.Account.Payer, p.GetPayer()) {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError, "payment daemon returned an invalid account admission")
		return
	}
	state := &SessionState{}
	state.SetReceiptMeta(ReceiptMeta{WorkID: p.GetAuthorizationId(), RequestID: requestID, CapabilityID: capability, OfferingID: offering, ExpectedMaxUnits: p.GetMaxTotalUnits()})
	ctx := context.WithValue(r.Context(), sessionStateKey{}, state)
	rec := &responseRecorder{ResponseWriter: w}
	rec.deferResponse()
	defer rec.commit()
	next.ServeHTTP(rec, r.WithContext(ctx))
	actual := rec.workUnits
	if h := rec.Header().Get(livepeerheader.WorkUnits); actual == 0 && h != "" {
		actual, _ = strconv.ParseUint(h, 10, 64)
	}
	if lc := state.LiveCounter(); lc != nil && lc.CurrentUnits() > actual {
		actual = lc.CurrentUnits()
	}
	billable := actual
	if billable > p.GetMaxTotalUnits() {
		billable = p.GetMaxTotalUnits()
	}
	settled, settleErr := account.SettleAuthorization(ctx, payment.SettleAuthorizationRequest{Payer: p.GetPayer(), AuthorizationID: p.GetAuthorizationId(), ActualUnits: billable, SettlementSeq: 1})
	if settleErr == nil && (settled == nil || settled.State != int32(paymentsv1.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED) || settled.Account == nil) {
		settleErr = errors.New("payment daemon returned invalid authorization settlement state")
	}
	if settleErr != nil {
		log.Printf("ERROR: authorization settlement FAILED authorization_id=%s units=%d: %v", p.GetAuthorizationId(), actual, settleErr)
		if slot := PendingDebitSlotFrom(r.Context()); slot != nil {
			slot.Set(&PendingDebit{AuthorizationBytes: authBytes, Sender: append([]byte(nil), p.GetPayer()...), WorkID: p.GetAuthorizationId(), DebitSeq: 1, ActualUnits: billable, MeasuredUnits: actual, WorkUnitName: spec.WorkUnit, JobID: rec.Header().Get(livepeerheader.JobID), RequestID: requestID, ReservedValueWei: admitted.Reserved, AccountFundingWei: admitted.Credited, AccountVersion: admitted.Account.Version})
		}
		rec.Header().Set(livepeerheader.Error, livepeerheader.ErrAccountingPending)
		rec.Header().Set(livepeerheader.WorkUnits, "0")
		return
	}
	rec.Header().Set(livepeerheader.WorkUnits, strconv.FormatUint(actual, 10))
	settlement := BuildAuthorizationSettlement(p, admitted.Reserved, settled.Billed, settled.Released, admitted.Credited, settled.Account.Version, actual, billable, rec.Header().Get(livepeerheader.JobID))
	if encoded, err := encode(settlement); err == nil {
		rec.Header().Set(livepeerheader.Settlement, encoded)
	} else {
		log.Printf("warning: authorization settlement encode failed: %v", err)
	}
	if receiptSink != nil && actual > 0 {
		revenue := settled.Billed.String()
		_ = receiptSink.UpsertWorkReceipt(ctx, receipts.WorkReceipt{ID: p.GetAuthorizationId(), RequestID: requestID, CapabilityID: capability, OfferingID: offering, ExpectedMaxUnits: p.GetMaxTotalUnits(), ActualUnits: actual, AcceptedWorkUnits: actual, GatewayRevenueWei: revenue, AttributedRevenueWei: revenue, Status: "final"})
	}
}

// ValidateCallerProof checks the optional delegated caller binding. The proof
// is a base64 65-byte secp256k1 EIP-191 signature over
// keccak256("livepeer-invocation-proof/v1\x00" || authorization_bytes).
// An empty caller key deliberately selects exact-scope bearer semantics.
func ValidateCallerProof(authorizationBytes, callerPublicKey []byte, encoded string) error {
	if len(callerPublicKey) == 0 {
		if encoded != "" {
			return errors.New("caller proof supplied but authorization has no caller_public_key")
		}
		return nil
	}
	if encoded == "" {
		return errors.New("missing Livepeer-Caller-Proof")
	}
	sig, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(sig) != crypto.SignatureLength || (sig[64] != 27 && sig[64] != 28) {
		return errors.New("Livepeer-Caller-Proof is malformed")
	}
	sig = append([]byte(nil), sig...)
	sig[64] -= 27
	digest := crypto.Keccak256(append([]byte("livepeer-invocation-proof/v1\x00"), authorizationBytes...))
	prefixed := crypto.Keccak256([]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(digest))), digest)
	pub, err := crypto.SigToPub(prefixed, sig)
	if err != nil {
		return errors.New("Livepeer-Caller-Proof signature is invalid")
	}
	got := crypto.FromECDSAPub(pub)
	want := callerPublicKey
	if len(want) == 33 {
		parsed, err := crypto.DecompressPubkey(want)
		if err != nil {
			return errors.New("caller_public_key is invalid")
		}
		want = crypto.FromECDSAPub(parsed)
	}
	if !bytes.Equal(got, want) {
		return errors.New("Livepeer-Caller-Proof does not match caller_public_key")
	}
	return nil
}

func BuildAuthorizationSettlement(p *paymentsv1.SpendAuthorizationPayload, reserved, billed, released, accountFunding *big.Int, accountVersion, actual, billedUnits uint64, jobID string) *paymentsv1.SettlementRecord {
	if reserved == nil {
		reserved = new(big.Int)
	}
	if billed == nil {
		billed = new(big.Int)
	}
	if released == nil {
		released = new(big.Int)
	}
	if accountFunding == nil {
		accountFunding = new(big.Int)
	}
	outcome := paymentsv1.SettlementRecord_EXACT
	if accountFunding.Sign() > 0 {
		outcome = paymentsv1.SettlementRecord_TOPPED_UP
	}
	if actual > billedUnits {
		outcome = paymentsv1.SettlementRecord_STOPPED_AT_BUDGET
	}
	return &paymentsv1.SettlementRecord{JobId: jobID, WorkId: p.GetAuthorizationId(), RequestId: p.GetRequestId(), IssuedAt: time.Now().UTC().Format(time.RFC3339Nano), AcceptedQuoteRef: p.GetAcceptedPrice().GetQuoteRef(), WorkUnitName: p.GetAcceptedPrice().GetWorkUnitName(), EstimatedUnits: p.GetMaxTotalUnits(), ActualUnits: actual, BilledUnits: billedUnits, DebitedUnits: billedUnits, FundedValueWei: &paymentsv1.BigUInt{Value: accountFunding.Bytes()}, BilledValueWei: &paymentsv1.BigUInt{Value: billed.Bytes()}, Outcome: outcome, AuthorizationId: p.GetAuthorizationId(), AuthorizedValueWei: p.GetMaxDebitWei(), ReservedValueWei: &paymentsv1.BigUInt{Value: reserved.Bytes()}, ReleasedValueWei: &paymentsv1.BigUInt{Value: released.Bytes()}, AccountFundingValueWei: &paymentsv1.BigUInt{Value: accountFunding.Bytes()}, AccountVersion: accountVersion}
}

func mapClientErr(err error) (int, string) {
	if errors.Is(err, errors.ErrUnsupported) {
		return http.StatusInternalServerError, livepeerheader.ErrInternalError
	}
	return http.StatusUnauthorized, livepeerheader.ErrPaymentInvalid
}
