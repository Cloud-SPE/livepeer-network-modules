package grpc

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	protocolv1 "github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts/livepeer/protocol/v1"
)

func TestPBRoundStatusFrom_AllFields(t *testing.T) {
	got := pbRoundStatusFrom(RoundStatus{
		LastRound:               7,
		LastIntentID:            []byte{0x01, 0x02},
		LastError:               "boom",
		CurrentRoundInitialized: true,
	})
	if got.GetLastRound() != 7 || got.GetLastError() != "boom" || !got.GetCurrentRoundInitialized() {
		t.Fatalf("scalar fields: %+v", got)
	}
	if !bytesEqual(got.GetLastIntentId(), []byte{0x01, 0x02}) {
		t.Fatalf("intent id = %x", got.GetLastIntentId())
	}
}

func TestPBTxIntentRefFrom(t *testing.T) {
	var id [32]byte
	for i := range id {
		id[i] = byte(i)
	}
	got := pbTxIntentRefFrom(TxIntentRef{ID: id})
	if !bytesEqual(got.GetId(), id[:]) {
		t.Fatalf("id mismatch: got %x; want %x", got.GetId(), id[:])
	}
}

func TestPBRewardStatusFrom_FullFields(t *testing.T) {
	addr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	in := RewardStatus{
		LastRound:         42,
		OrchAddress:       addr,
		Eligible:          true,
		EligibilityReason: "active=true",
		LastRewardRound:   41,
		Active:            true,
		LastIntentID:      []byte{0x01, 0x02},
		LastEarnedWei:     new(big.Int).SetUint64(1_000_000_000_000_000_000),
		LastError:         "",
	}
	got := pbRewardStatusFrom(in)
	if got.GetLastRound() != 42 || !got.GetEligible() || got.GetEligibilityReason() != "active=true" {
		t.Fatalf("scalar fields: %+v", got)
	}
	if len(got.GetOrchAddress()) != 20 {
		t.Fatalf("orch_address length = %d; want 20", len(got.GetOrchAddress()))
	}
	// big-endian unsigned bytes per the proto comment; 1e18 = 0x0DE0B6B3A7640000.
	wantWei := []byte{0x0D, 0xE0, 0xB6, 0xB3, 0xA7, 0x64, 0x00, 0x00}
	if g := got.GetLastEarnedWei(); !bytesEqual(g, wantWei) {
		t.Fatalf("last_earned_wei = %x; want %x", g, wantWei)
	}
}

func TestPBRewardStatusFrom_ZeroAddressOmitted(t *testing.T) {
	got := pbRewardStatusFrom(RewardStatus{LastRound: 1})
	if got.GetOrchAddress() != nil {
		t.Fatalf("zero address should produce nil bytes; got %x", got.GetOrchAddress())
	}
}

func TestPBTxIntentSnapshotFrom_NegativeTimesClamped(t *testing.T) {
	got := pbTxIntentSnapshotFrom(TxIntentSnapshot{ConfirmedAtUnixNano: -5})
	if got.GetConfirmedAtUnixNano() != 0 {
		t.Fatalf("negative time should clamp to 0; got %d", got.GetConfirmedAtUnixNano())
	}
}

func TestTxIntentRefFromPB_LengthValidation(t *testing.T) {
	if _, ok := txIntentRefFromPB(nil); ok {
		t.Fatal("nil ref should be rejected")
	}
	if _, ok := txIntentRefFromPB(&protocolv1.TxIntentRef{Id: make([]byte, 31)}); ok {
		t.Fatal("31-byte id should be rejected")
	}
	if _, ok := txIntentRefFromPB(&protocolv1.TxIntentRef{Id: make([]byte, 33)}); ok {
		t.Fatal("33-byte id should be rejected")
	}
	if _, ok := txIntentRefFromPB(&protocolv1.TxIntentRef{Id: make([]byte, 32)}); !ok {
		t.Fatal("32-byte id should be accepted")
	}
}

func TestPBForceOutcomeFrom_Submitted(t *testing.T) {
	var id [32]byte
	for i := range id {
		id[i] = byte(i + 1)
	}
	got := pbForceOutcomeFrom(ForceOutcome{Submitted: &TxIntentRef{ID: id}})
	if got.GetSkipped() != nil {
		t.Fatalf("expected no Skipped arm; got %+v", got.GetSkipped())
	}
	if got.GetSubmitted() == nil {
		t.Fatal("expected Submitted arm")
	}
	if !bytesEqual(got.GetSubmitted().GetId(), id[:]) {
		t.Fatalf("submitted id mismatch: got %x want %x", got.GetSubmitted().GetId(), id[:])
	}
}

func TestPBForceOutcomeFrom_Skipped(t *testing.T) {
	cases := []struct {
		name   string
		code   SkipCode
		reason string
		want   protocolv1.SkipReason_Code
	}{
		{"already_rewarded", SkipCodeAlreadyRewarded, "already rewarded this round", protocolv1.SkipReason_CODE_ALREADY_REWARDED},
		{"transcoder_inactive", SkipCodeTranscoderInactive, "transcoder is not active at this round", protocolv1.SkipReason_CODE_TRANSCODER_INACTIVE},
		{"round_initialized", SkipCodeRoundInitialized, "round already initialized", protocolv1.SkipReason_CODE_ROUND_INITIALIZED},
		{"unspecified", SkipCodeUnspecified, "ineligible", protocolv1.SkipReason_CODE_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pbForceOutcomeFrom(ForceOutcome{Skipped: &SkipReason{Reason: tc.reason, Code: tc.code}})
			if got.GetSubmitted() != nil {
				t.Fatalf("expected no Submitted arm; got %+v", got.GetSubmitted())
			}
			if got.GetSkipped() == nil {
				t.Fatal("expected Skipped arm")
			}
			if got.GetSkipped().GetCode() != tc.want {
				t.Fatalf("code = %v; want %v", got.GetSkipped().GetCode(), tc.want)
			}
			if got.GetSkipped().GetReason() != tc.reason {
				t.Fatalf("reason = %q; want %q", got.GetSkipped().GetReason(), tc.reason)
			}
		})
	}
}

func TestPBForceOutcomeFrom_ZeroValueDefensive(t *testing.T) {
	got := pbForceOutcomeFrom(ForceOutcome{})
	// Zero-value must not produce an unset oneof — TS clients treat that
	// as undefined behavior. Map to Skipped{CODE_UNSPECIFIED}.
	if got.GetSubmitted() != nil {
		t.Fatalf("zero-value should not produce Submitted arm; got %+v", got.GetSubmitted())
	}
	if got.GetSkipped() == nil {
		t.Fatal("zero-value should produce Skipped arm")
	}
	if got.GetSkipped().GetCode() != protocolv1.SkipReason_CODE_UNSPECIFIED {
		t.Fatalf("code = %v; want CODE_UNSPECIFIED", got.GetSkipped().GetCode())
	}
}

func TestErrorToStatus(t *testing.T) {
	cases := []struct {
		name string
		in   error
		want codes.Code
	}{
		{"nil", nil, codes.OK},
		{"unimplemented", ErrUnimplemented, codes.Unimplemented},
		{"unimplemented_wrapped", errors.New("wrap: " + ErrUnimplemented.Error()), codes.Internal}, // not Is(); wrapping requires errors.Is chain
		{"unimplemented_is", wrapErr(ErrUnimplemented), codes.Unimplemented},
		{"not_found", ErrNotFound, codes.NotFound},
		{"not_found_is", wrapErr(ErrNotFound), codes.NotFound},
		{"generic", errors.New("boom"), codes.Internal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := errorToStatus(tc.in)
			if tc.in == nil {
				if got != nil {
					t.Fatalf("nil in → %v", got)
				}
				return
			}
			if c := status.Code(got); c != tc.want {
				t.Fatalf("code = %v; want %v (got=%v)", c, tc.want, got)
			}
		})
	}
}

// wrapErr wraps an error so errors.Is can still find it.
func wrapErr(e error) error { return wrapped{e: e} }

type wrapped struct{ e error }

func (w wrapped) Error() string { return "wrapped: " + w.e.Error() }
func (w wrapped) Unwrap() error { return w.e }

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
