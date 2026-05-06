package web

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/canonical"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/signing"
)

// canonicalManifestBytes accepts either a bare manifest object or an
// outer envelope `{manifest, signature}` shape and returns canonical
// bytes of the inner manifest. The candidate form coordinator uploads
// is the inner manifest alone; the last-signed file is the full
// envelope.
func canonicalManifestBytes(raw []byte) ([]byte, error) {
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if inner, ok := probe["manifest"].(map[string]any); ok {
		return canonical.Bytes(inner)
	}
	return canonical.BytesFromJSON(raw)
}

func signCandidate(manifestBytes []byte, signer signing.Signer) ([]byte, error) {
	canon, err := canonicalManifestBytes(manifestBytes)
	if err != nil {
		return nil, err
	}
	sig, err := signer.SignCanonical(canon)
	if err != nil {
		return nil, err
	}
	var inner map[string]any
	probe := map[string]any{}
	if err := json.Unmarshal(manifestBytes, &probe); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if m, ok := probe["manifest"].(map[string]any); ok {
		inner = m
	} else {
		inner = probe
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
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}
	return out, nil
}

func publicationSeq(manifestBytes []byte) *uint64 {
	var probe map[string]any
	if err := json.Unmarshal(manifestBytes, &probe); err != nil {
		return nil
	}
	inner, ok := probe["manifest"].(map[string]any)
	if !ok {
		inner = probe
	}
	switch v := inner["publication_seq"].(type) {
	case float64:
		if v < 0 {
			return nil
		}
		n := uint64(v)
		return &n
	case json.Number:
		i, err := v.Int64()
		if err != nil || i < 0 {
			return nil
		}
		n := uint64(i)
		return &n
	}
	return nil
}

func lastFourHex(addr string) string {
	addr = strings.ToLower(strings.TrimSpace(addr))
	addr = strings.TrimPrefix(addr, "0x")
	if len(addr) < 4 {
		return ""
	}
	return addr[len(addr)-4:]
}

var errEmptyEnvelope = errors.New("empty envelope")

func summarizeEnvelope(envelope []byte) envelopeSummary {
	if len(envelope) == 0 {
		return envelopeSummary{}
	}
	var probe map[string]any
	if err := json.Unmarshal(envelope, &probe); err != nil {
		return envelopeSummary{Error: err.Error()}
	}
	inner, ok := probe["manifest"].(map[string]any)
	if !ok {
		return envelopeSummary{Error: errEmptyEnvelope.Error()}
	}
	out := envelopeSummary{}
	if seq := publicationSeq(envelope); seq != nil {
		out.PublicationSeq = *seq
	}
	if addr, ok := inner["orch"].(map[string]any); ok {
		out.EthAddress, _ = addr["eth_address"].(string)
	}
	out.IssuedAt, _ = inner["issued_at"].(string)
	out.ExpiresAt, _ = inner["expires_at"].(string)
	if caps, ok := inner["capabilities"].([]any); ok {
		out.CapabilityCount = len(caps)
	}
	return out
}
