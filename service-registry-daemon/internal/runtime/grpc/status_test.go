package grpc

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestErrorToStatus_Nil(t *testing.T) {
	if errorToStatus(nil) != nil {
		t.Fatal("nil → nil")
	}
}

func TestErrorToStatus_AllSentinels(t *testing.T) {
	cases := []struct {
		err      error
		wantCode codes.Code
		wantStr  string
	}{
		{types.ErrNotFound, codes.NotFound, "not_found"},
		{types.ErrManifestUnavailable, codes.Unavailable, "manifest_unavailable"},
		{types.ErrSignatureMismatch, codes.Unauthenticated, "signature_mismatch"},
		{types.ErrParse, codes.InvalidArgument, "parse_error"},
		{types.ErrManifestTooLarge, codes.ResourceExhausted, "manifest_too_large"},
		{types.ErrChainUnavailable, codes.Unavailable, "chain_unavailable"},
		{types.ErrUnknownMode, codes.InvalidArgument, "unknown_mode"},
		{types.ErrCacheStaleFailing, codes.DeadlineExceeded, "cache_stale_failing"},
		{types.ErrKeystoreLocked, codes.FailedPrecondition, "keystore_locked"},
		{types.ErrChainWriteFailed, codes.FailedPrecondition, "chain_write_failed"},
		{types.ErrChainWriteNotImpl, codes.FailedPrecondition, "chain_write_failed"},
		{types.ErrInvalidEthAddress, codes.InvalidArgument, "parse_error"},
		{types.ErrInvalidSchemaVersion, codes.InvalidArgument, "parse_error"},
		{types.ErrEmptyNodes, codes.InvalidArgument, "parse_error"},
		{types.ErrInvalidNodeURL, codes.InvalidArgument, "parse_error"},
		{types.ErrSignatureMalformed, codes.InvalidArgument, "parse_error"},
		{types.ErrManifestExpired, codes.InvalidArgument, "parse_error"},
		{errors.New("random"), codes.Internal, "internal"},
	}
	for _, c := range cases {
		t.Run(c.err.Error(), func(t *testing.T) {
			grpcErr := errorToStatus(c.err)
			st, ok := status.FromError(grpcErr)
			if !ok {
				t.Fatalf("not a status: %v", grpcErr)
			}
			if st.Code() != c.wantCode {
				t.Fatalf("code = %s, want %s", st.Code(), c.wantCode)
			}
			if got := extractCode(grpcErr); got != c.wantStr {
				t.Fatalf("registry code = %q, want %q", got, c.wantStr)
			}
		})
	}
}

func TestErrorToStatus_WrapsUnwrap(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", types.ErrManifestUnavailable)
	st, ok := status.FromError(errorToStatus(wrapped))
	if !ok {
		t.Fatal("not a status")
	}
	if st.Code() != codes.Unavailable {
		t.Fatalf("expected Unavailable, got %s", st.Code())
	}
	if extractCode(errorToStatus(wrapped)) != "manifest_unavailable" {
		t.Fatal("expected manifest_unavailable code on wrapped error")
	}
}

func TestExtractCode_NonStatus(t *testing.T) {
	if got := extractCode(errors.New("plain")); got != "" {
		t.Fatalf("expected empty code, got %s", got)
	}
}

func TestCodeDetail_Empty(t *testing.T) {
	if codeDetail("") != nil {
		t.Fatal("empty code should produce nil detail")
	}
}
