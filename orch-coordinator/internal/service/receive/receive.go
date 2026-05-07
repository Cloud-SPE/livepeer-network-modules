// Package receive verifies a signed manifest upload and atomic-swap
// publishes it. The five-step verify pipeline (plan 0018 §7):
//
//   1. Schema-valid against the manifest schema.
//   2. Signature recovers to the configured eth_address.
//   3. manifest.orch.eth_address matches the operator identity.
//   4. spec_version matches the most-recent candidate the
//      coordinator built.
//   5. issued_at and expires_at are well-formed and in the future,
//      and publication_seq is strictly greater than the currently-
//      published manifest's value.
//
// On success: take the publish lock, atomic-swap publish, append an
// audit event with outcome=accepted. On any failure: append an audit
// event with the corresponding outcome code; the currently-published
// manifest stays live.
package receive

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/verify"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/published"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/candidate"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

// VerifyError is returned when verification fails. It carries the
// stable error code recorded in the audit log + Prometheus.
type VerifyError struct {
	Code audit.Outcome
	Msg  string
}

func (e *VerifyError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Msg) }

// Observer is the metrics hook for upload + verify + publish.
type Observer interface {
	ObserveUpload(outcome string, dur time.Duration)
	ObservePublish(outcome string)
	ObserveVerifyDuration(dur time.Duration)
}

// Service ties the verifier, publish store, audit log, and the
// candidate builder (for spec_version drift checks) together.
type Service struct {
	verifier   verify.Verifier
	store      *published.Store
	audit      *audit.Log
	expectAddr string
	specVer    string
	observer   Observer
}

// SetObserver attaches a metrics observer.
func (s *Service) SetObserver(o Observer) { s.observer = o }

// New builds a Service.
func New(store *published.Store, audit *audit.Log, expectAddr, specVer string) *Service {
	return &Service{
		verifier:   verify.New(),
		store:      store,
		audit:      audit,
		expectAddr: strings.ToLower(strings.TrimSpace(expectAddr)),
		specVer:    specVer,
	}
}

// Result is what Receive returns on success or partial-success
// (verify-only).
type Result struct {
	SignedManifest  *types.SignedManifest
	ManifestSHA256  string
	SignatureSHA256 string
	PublicationSeq  uint64
}

// Receive runs the five-step verify and (on success) atomic-swap
// publishes. The caller passes the raw multipart-upload body.
func (s *Service) Receive(body []byte, uploader string) (*Result, error) {
	start := time.Now()
	defer func() {
		if s.observer != nil {
			s.observer.ObserveVerifyDuration(time.Since(start))
		}
	}()
	res, err := s.receiveInner(body, uploader)
	if s.observer != nil {
		if err != nil {
			var ve *VerifyError
			if errors.As(err, &ve) {
				s.observer.ObserveUpload(string(ve.Code), time.Since(start))
				if ve.Code == audit.OutcomePublishFailed {
					s.observer.ObservePublish(string(ve.Code))
				}
			} else {
				s.observer.ObserveUpload("internal_error", time.Since(start))
			}
		} else {
			s.observer.ObserveUpload(string(audit.OutcomeAccepted), time.Since(start))
			s.observer.ObservePublish(string(audit.OutcomeAccepted))
		}
	}
	return res, err
}

func (s *Service) receiveInner(body []byte, uploader string) (*Result, error) {
	sm, err := types.ParseSignedManifest(body)
	if err != nil {
		s.recordFailure(audit.OutcomeSchemaInvalid, uploader, "", "", 0, err.Error())
		return nil, &VerifyError{Code: audit.OutcomeSchemaInvalid, Msg: err.Error()}
	}

	canonical, err := candidate.CanonicalBytes(manifestPayloadMap(sm.Manifest))
	if err != nil {
		s.recordFailure(audit.OutcomeSchemaInvalid, uploader, "", "", sm.Manifest.PublicationSeq, err.Error())
		return nil, &VerifyError{Code: audit.OutcomeSchemaInvalid, Msg: err.Error()}
	}
	manifestHash := candidate.SHA256Hex(canonical)
	sigHash := candidate.SHA256Hex([]byte(sm.Signature.Value))

	if err := schemaCheck(sm); err != nil {
		s.recordFailure(audit.OutcomeSchemaInvalid, uploader, manifestHash, sigHash, sm.Manifest.PublicationSeq, err.Error())
		return nil, &VerifyError{Code: audit.OutcomeSchemaInvalid, Msg: err.Error()}
	}

	sigBytes, err := decodeSignature(sm.Signature.Value)
	if err != nil {
		s.recordFailure(audit.OutcomeSigInvalid, uploader, manifestHash, sigHash, sm.Manifest.PublicationSeq, err.Error())
		return nil, &VerifyError{Code: audit.OutcomeSigInvalid, Msg: err.Error()}
	}

	got, err := s.verifier.Recover(canonical, sigBytes)
	if err != nil {
		s.recordFailure(audit.OutcomeSigInvalid, uploader, manifestHash, sigHash, sm.Manifest.PublicationSeq, err.Error())
		return nil, &VerifyError{Code: audit.OutcomeSigInvalid, Msg: err.Error()}
	}
	if !strings.EqualFold(strings.TrimSpace(got.String()), s.expectAddr) {
		msg := fmt.Sprintf("recovered %s != configured %s", got, s.expectAddr)
		s.recordFailure(audit.OutcomeSigInvalid, uploader, manifestHash, sigHash, sm.Manifest.PublicationSeq, msg)
		return nil, &VerifyError{Code: audit.OutcomeSigInvalid, Msg: msg}
	}

	if !strings.EqualFold(strings.TrimSpace(sm.Manifest.Orch.EthAddress), s.expectAddr) {
		msg := fmt.Sprintf("manifest.orch.eth_address %s != configured %s", sm.Manifest.Orch.EthAddress, s.expectAddr)
		s.recordFailure(audit.OutcomeIdentityMismatch, uploader, manifestHash, sigHash, sm.Manifest.PublicationSeq, msg)
		return nil, &VerifyError{Code: audit.OutcomeIdentityMismatch, Msg: msg}
	}

	if s.specVer != "" && sm.Manifest.SpecVersion != s.specVer {
		msg := fmt.Sprintf("spec_version drift: signed=%s candidate=%s", sm.Manifest.SpecVersion, s.specVer)
		s.recordFailure(audit.OutcomeDriftRejected, uploader, manifestHash, sigHash, sm.Manifest.PublicationSeq, msg)
		return nil, &VerifyError{Code: audit.OutcomeDriftRejected, Msg: msg}
	}

	now := time.Now().UTC()
	if sm.Manifest.IssuedAt.IsZero() {
		msg := "issued_at missing"
		s.recordFailure(audit.OutcomeWindowInvalid, uploader, manifestHash, sigHash, sm.Manifest.PublicationSeq, msg)
		return nil, &VerifyError{Code: audit.OutcomeWindowInvalid, Msg: msg}
	}
	if sm.Manifest.ExpiresAt.Before(now) || sm.Manifest.ExpiresAt.Equal(now) {
		msg := fmt.Sprintf("expires_at %s is not in the future (now %s)", sm.Manifest.ExpiresAt, now)
		s.recordFailure(audit.OutcomeWindowInvalid, uploader, manifestHash, sigHash, sm.Manifest.PublicationSeq, msg)
		return nil, &VerifyError{Code: audit.OutcomeWindowInvalid, Msg: msg}
	}

	if err := s.checkPublicationSeq(sm.Manifest.PublicationSeq); err != nil {
		s.recordFailure(audit.OutcomeRollbackRejected, uploader, manifestHash, sigHash, sm.Manifest.PublicationSeq, err.Error())
		return nil, &VerifyError{Code: audit.OutcomeRollbackRejected, Msg: err.Error()}
	}

	if err := s.publish(body); err != nil {
		s.recordFailure(audit.OutcomePublishFailed, uploader, manifestHash, sigHash, sm.Manifest.PublicationSeq, err.Error())
		return nil, &VerifyError{Code: audit.OutcomePublishFailed, Msg: err.Error()}
	}

	if _, err := s.audit.Append(audit.Event{
		At:              now,
		Outcome:         audit.OutcomeAccepted,
		Uploader:        uploader,
		SignatureSHA256: sigHash,
		ManifestSHA256:  manifestHash,
		PublicationSeq:  sm.Manifest.PublicationSeq,
		ErrorCode:       "",
	}); err != nil {
		return nil, fmt.Errorf("audit append accepted: %w", err)
	}

	return &Result{
		SignedManifest:  sm,
		ManifestSHA256:  manifestHash,
		SignatureSHA256: sigHash,
		PublicationSeq:  sm.Manifest.PublicationSeq,
	}, nil
}

func (s *Service) checkPublicationSeq(incoming uint64) error {
	body, _, err := s.store.Read()
	if err != nil {
		if errors.Is(err, published.ErrEmpty) {
			return nil
		}
		return err
	}
	cur, err := types.ParseSignedManifest(body)
	if err != nil {
		// Live manifest unparseable: accept the new one (anti-rollback
		// can't apply if the old bytes are corrupt; the operator's
		// reaction is to publish a known-good manifest).
		return nil
	}
	if incoming <= cur.Manifest.PublicationSeq {
		return fmt.Errorf("publication_seq %d <= currently-published %d", incoming, cur.Manifest.PublicationSeq)
	}
	return nil
}

func (s *Service) publish(body []byte) error {
	if err := s.store.Lock(); err != nil {
		return fmt.Errorf("publish lock: %w", err)
	}
	defer s.store.Unlock()
	return s.store.Publish(body)
}

func (s *Service) recordFailure(code audit.Outcome, uploader, manifestHash, sigHash string, pubSeq uint64, msg string) {
	_, _ = s.audit.Append(audit.Event{
		At:              time.Now().UTC(),
		Outcome:         code,
		Uploader:        uploader,
		SignatureSHA256: sigHash,
		ManifestSHA256:  manifestHash,
		PublicationSeq:  pubSeq,
		ErrorCode:       string(code),
		Note:            msg,
	})
}

func schemaCheck(sm *types.SignedManifest) error {
	if sm.Manifest.SpecVersion == "" {
		return errors.New("manifest.spec_version: required")
	}
	if !strings.HasPrefix(strings.TrimSpace(sm.Manifest.Orch.EthAddress), "0x") {
		return errors.New("manifest.orch.eth_address: must be 0x-prefixed")
	}
	if sm.Signature.Algorithm != "secp256k1" {
		return fmt.Errorf("signature.algorithm: must be secp256k1, got %q", sm.Signature.Algorithm)
	}
	if !strings.HasPrefix(sm.Signature.Value, "0x") {
		return errors.New("signature.value: must be 0x-prefixed hex")
	}
	for _, c := range sm.Manifest.Capabilities {
		if c.CapabilityID == "" || c.OfferingID == "" {
			return errors.New("capability: capability_id + offering_id required")
		}
		if c.WorkUnit.Name == "" {
			return errors.New("capability.work_unit.name: required")
		}
		if c.PricePerUnitWei == "" {
			return errors.New("capability.price_per_unit_wei: required")
		}
		if !strings.HasPrefix(c.WorkerURL, "https://") {
			return fmt.Errorf("capability.worker_url: must be https://, got %q", c.WorkerURL)
		}
	}
	return nil
}

func decodeSignature(s string) ([]byte, error) {
	if !strings.HasPrefix(s, "0x") {
		return nil, errors.New("missing 0x prefix")
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil {
		return nil, fmt.Errorf("hex decode: %w", err)
	}
	if len(b) != 65 {
		return nil, fmt.Errorf("expected 65-byte signature, got %d", len(b))
	}
	return b, nil
}

// manifestPayloadMap builds the byte-identical map representation of
// the signed manifest payload that the candidate builder uses. The
// canonicalization MUST match what secure-orch signed.
func manifestPayloadMap(p types.ManifestPayload) map[string]any {
	root := map[string]any{
		"spec_version":    p.SpecVersion,
		"publication_seq": p.PublicationSeq,
		"issued_at":       p.IssuedAt.UTC().Format(time.RFC3339Nano),
		"expires_at":      p.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"orch":            orchToMap(p.Orch),
		"capabilities":    capsToList(p.Capabilities),
	}
	return root
}

func orchToMap(o types.Orch) map[string]any {
	m := map[string]any{"eth_address": o.EthAddress}
	if o.ServiceURI != "" {
		m["service_uri"] = o.ServiceURI
	}
	return m
}

func capsToList(caps []types.CapabilityTuple) []any {
	out := make([]any, 0, len(caps))
	for _, c := range caps {
		entry := map[string]any{
			"capability_id":      c.CapabilityID,
			"offering_id":        c.OfferingID,
			"interaction_mode":   c.InteractionMode,
			"work_unit":          map[string]any{"name": c.WorkUnit.Name},
			"price_per_unit_wei": c.PricePerUnitWei,
			"worker_url":         c.WorkerURL,
		}
		if len(c.Extra) > 0 {
			entry["extra"] = c.Extra
		}
		if len(c.Constraints) > 0 {
			entry["constraints"] = c.Constraints
		}
		out = append(out, entry)
	}
	return out
}
