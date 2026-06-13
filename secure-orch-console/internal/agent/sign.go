package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/canonical"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/signing"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ApplySeqDiscipline rewrites the candidate's publication_seq per
// plan 0042 §6 step 6: the cold side owns the canonical seq, so the
// signed seq is max(candidate seq, last-signed seq + 1) — never
// reused, never decreased. lastSignedEnvelope may be nil (first sign
// cycle). Returns the updated manifest bytes and the resolved seq.
// Shared with the console's held-queue approve flow so an operator
// approval signs exactly what the agent would have.
func ApplySeqDiscipline(manifestBytes, lastSignedEnvelope []byte) ([]byte, uint64, error) {
	var lastSeq *uint64
	if lastSignedEnvelope != nil {
		seq, _, err := envelopeSeqAndHash(lastSignedEnvelope)
		if err != nil {
			return nil, 0, fmt.Errorf("agent: last-signed: %w", err)
		}
		lastSeq = &seq
	}
	return applySeqDiscipline(manifestBytes, lastSeq)
}

func applySeqDiscipline(manifestBytes []byte, lastSignedSeq *uint64) ([]byte, uint64, error) {
	var inner map[string]any
	if err := json.Unmarshal(manifestBytes, &inner); err != nil {
		return nil, 0, fmt.Errorf("agent: decode candidate manifest: %w", err)
	}
	seq, _, err := envelopeSeqAndHash(manifestBytes)
	if err != nil {
		return nil, 0, err
	}
	if lastSignedSeq != nil && seq <= *lastSignedSeq {
		seq = *lastSignedSeq + 1
	}
	inner["publication_seq"] = seq
	out, err := json.Marshal(inner)
	if err != nil {
		return nil, 0, fmt.Errorf("agent: marshal manifest: %w", err)
	}
	return out, seq, nil
}

// signCandidate signs the candidate manifest with sequence
// discipline applied. Returns the envelope bytes and the seq signed.
func signCandidate(manifestBytes []byte, lastSignedSeq *uint64, signer signing.Signer) ([]byte, uint64, error) {
	updated, seq, err := applySeqDiscipline(manifestBytes, lastSignedSeq)
	if err != nil {
		return nil, 0, err
	}
	var inner map[string]any
	if err := json.Unmarshal(updated, &inner); err != nil {
		return nil, 0, fmt.Errorf("agent: decode candidate manifest: %w", err)
	}

	canon, err := canonical.Bytes(inner)
	if err != nil {
		return nil, 0, fmt.Errorf("agent: canonicalize: %w", err)
	}
	sig, err := signer.SignCanonical(canon)
	if err != nil {
		return nil, 0, fmt.Errorf("agent: sign: %w", err)
	}
	envelope := map[string]any{
		"manifest": inner,
		"signature": map[string]any{
			"algorithm":        "secp256k1",
			"value":            "0x" + hex.EncodeToString(sig),
			"canonicalization": "JCS",
		},
	}
	out, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, 0, fmt.Errorf("agent: marshal envelope: %w", err)
	}
	return out, seq, nil
}

// expirySplit extracts (issued_at, expires_at) from a manifest or
// envelope. Used to derive the manifest TTL and remaining validity
// for the renewal threshold.
func expirySplit(raw []byte) (issuedAt, expiresAt string) {
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", ""
	}
	inner := probe
	if m, ok := probe["manifest"].(map[string]any); ok {
		inner = m
	}
	issuedAt, _ = inner["issued_at"].(string)
	expiresAt, _ = inner["expires_at"].(string)
	return issuedAt, expiresAt
}
