package grpc

import (
	"context"
	"errors"
	registryv1 "github.com/Cloud-SPE/livepeer-network-modules/proto-contracts/livepeer/registry/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"
	"google.golang.org/protobuf/types/known/structpb"
)

// errorToStatus maps a domain error to a gRPC status. The stable error
// code string from docs/product-specs/grpc-surface.md is attached as a
// status detail so consumers can match category without parsing English.
//
// nil → nil (caller short-circuits).
func errorToStatus(err error) error {
	if err == nil {
		return nil
	}
	code, sentinel := classifyError(err)
	st := status.New(code, err.Error())
	details := []protoadapt.MessageV1{protoadapt.MessageV1Of(codeDetail(sentinel))}
	var resolution *types.ResolutionError
	if errors.As(err, &resolution) {
		details = append(details, protoadapt.MessageV1Of(&registryv1.RegistryResolutionDetail{EthAddress: string(resolution.Address), DiscoveryStatus: discoveryStatusToProto(resolution.Status), EvaluatedAt: timeToProto(resolution.EvaluatedAt)}))
		if resolution.RetryAfter > 0 {
			details = append(details, &errdetails.RetryInfo{RetryDelay: durationpb.New(resolution.RetryAfter)})
		}
	}
	if with, addErr := st.WithDetails(details...); addErr == nil {
		return with.Err()
	}

	return st.Err()
}

// classifyError returns (gRPC code, the sentinel string used in status detail).
func classifyError(err error) (codes.Code, string) {
	switch {
	case errors.Is(err, context.Canceled):
		return codes.Canceled, "canceled"
	case errors.Is(err, types.ErrResolutionDeferred):
		return codes.Unavailable, "resolution_deferred"
	case errors.Is(err, context.DeadlineExceeded):
		return codes.DeadlineExceeded, "deadline_exceeded"
	case errors.Is(err, types.ErrManifestUnsupported):
		return codes.FailedPrecondition, "manifest_unsupported"
	case errors.Is(err, types.ErrRegistryUnavailable):
		return codes.Unavailable, "registry_unavailable"
	case errors.Is(err, types.ErrNotFound):
		return codes.NotFound, "not_found"
	case errors.Is(err, types.ErrManifestUnavailable):
		return codes.Unavailable, "manifest_unavailable"
	case errors.Is(err, types.ErrSignatureMismatch):
		return codes.Unauthenticated, "signature_mismatch"
	case errors.Is(err, types.ErrParse):
		return codes.InvalidArgument, "parse_error"
	case errors.Is(err, types.ErrUnknownField):
		return codes.InvalidArgument, "unknown_field"
	case errors.Is(err, types.ErrManifestTooLarge):
		return codes.ResourceExhausted, "manifest_too_large"
	case errors.Is(err, types.ErrChainUnavailable):
		return codes.Unavailable, "chain_unavailable"
	case errors.Is(err, types.ErrUnknownMode):
		return codes.InvalidArgument, "unknown_mode"
	case errors.Is(err, types.ErrCacheStaleFailing):
		return codes.DeadlineExceeded, "cache_stale_failing"
	case errors.Is(err, types.ErrKeystoreLocked):
		return codes.FailedPrecondition, "keystore_locked"
	case errors.Is(err, types.ErrChainWriteFailed), errors.Is(err, types.ErrChainWriteNotImpl):
		return codes.FailedPrecondition, "chain_write_failed"
	case errors.Is(err, types.ErrInvalidEthAddress):
		return codes.InvalidArgument, "parse_error"
	case errors.Is(err, types.ErrInvalidSchemaVersion),
		errors.Is(err, types.ErrEmptyNodes),
		errors.Is(err, types.ErrInvalidNodeURL),
		errors.Is(err, types.ErrSignatureMalformed),
		errors.Is(err, types.ErrManifestExpired):
		return codes.InvalidArgument, "parse_error"
	default:
		return codes.Internal, "internal"
	}
}

// codeDetail wraps the stable code string in a structpb.Struct so it
// rides on the gRPC status detail and survives across language boundaries.
// Consumers extract via status.FromError(err).Details(); the proto type
// returned is *structpb.Struct.
func codeDetail(code string) *structpb.Struct {
	if code == "" {
		return nil
	}
	s, err := structpb.NewStruct(map[string]interface{}{
		"registry_error_code": code,
	})
	if err != nil {
		return nil
	}
	return s
}

// extractCode is the symmetric helper used by tests / consumers to pull
// the stable string back out of a gRPC error.
func extractCode(err error) string {
	st, ok := status.FromError(err)
	if !ok {
		return ""
	}
	for _, d := range st.Details() {
		s, ok := d.(*structpb.Struct)
		if !ok {
			continue
		}
		if v, ok := s.Fields["registry_error_code"]; ok {
			return v.GetStringValue()
		}
	}
	return ""
}
