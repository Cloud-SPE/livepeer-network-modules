package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/canonical"
)

// maxCandidateBytes bounds every byte pulled from the coordinator —
// the candidate is untrusted input until it survives strict parsing
// (plan 0042 §6 step 2). Mirrors the coordinator's own 1 MiB upload
// ceiling with headroom for the metadata sidecar.
const maxCandidateBytes = 4 << 20

// Client is the agent's outbound-only HTTP surface: pull candidates
// from the coordinator admin listener, push signed manifests back,
// confirm against the public well-known route. All transport is
// initiated from the secure host (hard constraint #1).
type Client struct {
	// AdminURL is the coordinator's operator listener base
	// (candidate download + signed-manifest upload).
	AdminURL string
	// PublicURL is the coordinator's resolver-facing listener base
	// (publish confirmation).
	PublicURL string
	// Token is the agent bearer credential.
	Token string
	// HTTP is the underlying client; caller sets the timeout.
	HTTP *http.Client
}

// Candidate is a strictly-validated pull result.
type Candidate struct {
	ETag          string
	ManifestBytes []byte
	MetadataBytes []byte
	// CanonicalSHA256 is the hex digest of the JCS-canonical inner
	// manifest bytes — the bytes the cold key would sign.
	CanonicalSHA256 string
	PublicationSeq  uint64
}

// ErrNoCandidate is returned while the coordinator has not built a
// candidate yet (503 on the candidate routes).
var ErrNoCandidate = errors.New("agent: coordinator has no candidate yet")

// FetchCandidate conditionally pulls /candidate.tar.gz. A 304 returns
// (nil, false, nil) — sleeping is the caller's business. The tarball
// is validated strictly: exactly the expected members, valid JSON,
// canonicalizable manifest.
func (c *Client) FetchCandidate(ctx context.Context, etag string) (*Candidate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.AdminURL+"/candidate.tar.gz", nil)
	if err != nil {
		return nil, err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	c.authorize(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: fetch candidate: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil, nil
	case http.StatusServiceUnavailable:
		return nil, ErrNoCandidate
	case http.StatusOK:
	default:
		return nil, fmt.Errorf("agent: fetch candidate: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCandidateBytes+1))
	if err != nil {
		return nil, fmt.Errorf("agent: read candidate: %w", err)
	}
	if len(body) > maxCandidateBytes {
		return nil, fmt.Errorf("agent: candidate exceeds %d bytes", maxCandidateBytes)
	}
	cand, err := parseCandidateTarball(body)
	if err != nil {
		return nil, err
	}
	cand.ETag = resp.Header.Get("ETag")
	return cand, nil
}

// PushSigned posts the signed envelope. The coordinator's five-step
// verify pipeline is the real gate; this just reports its verdict.
func (c *Client) PushSigned(ctx context.Context, envelope []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.AdminURL+"/admin/signed-manifest", bytes.NewReader(envelope))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("agent: push signed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent: push signed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// Published is the publish-confirmation view of the public manifest.
type Published struct {
	PublicationSeq  uint64
	CanonicalSHA256 string
	// IssuedAt/ExpiresAt feed the expiry gauge and warning; zero when
	// unparseable.
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// ErrNotPublished is returned while the public route serves nothing.
var ErrNotPublished = errors.New("agent: nothing published yet")

// FetchPublished reads the public well-known manifest and reduces it
// to (seq, canonical-bytes hash) for confirmation (§8).
func (c *Client) FetchPublished(ctx context.Context) (Published, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.PublicURL+"/.well-known/livepeer-registry.json", nil)
	if err != nil {
		return Published{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Published{}, fmt.Errorf("agent: fetch published: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusServiceUnavailable {
		return Published{}, ErrNotPublished
	}
	if resp.StatusCode != http.StatusOK {
		return Published{}, fmt.Errorf("agent: fetch published: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCandidateBytes))
	if err != nil {
		return Published{}, fmt.Errorf("agent: read published: %w", err)
	}
	seq, sha, err := envelopeSeqAndHash(body)
	if err != nil {
		return Published{}, fmt.Errorf("agent: published manifest: %w", err)
	}
	pub := Published{PublicationSeq: seq, CanonicalSHA256: sha}
	issuedStr, expiresStr := expirySplit(body)
	if t, err := time.Parse(time.RFC3339Nano, issuedStr); err == nil {
		pub.IssuedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, expiresStr); err == nil {
		pub.ExpiresAt = t
	}
	return pub, nil
}

func (c *Client) authorize(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

func parseCandidateTarball(body []byte) (*Candidate, error) {
	gr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("agent: candidate gzip: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	cand := &Candidate{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("agent: candidate tar: %w", err)
		}
		member, err := io.ReadAll(io.LimitReader(tr, maxCandidateBytes))
		if err != nil {
			return nil, fmt.Errorf("agent: candidate tar member %s: %w", hdr.Name, err)
		}
		switch hdr.Name {
		case "manifest.json":
			cand.ManifestBytes = member
		case "metadata.json":
			cand.MetadataBytes = member
		default:
			// Every pulled byte is untrusted input; an unexpected
			// member is a malformed candidate, not a curiosity.
			return nil, fmt.Errorf("agent: candidate tar: unexpected member %q", hdr.Name)
		}
	}
	if cand.ManifestBytes == nil {
		return nil, errors.New("agent: candidate tar missing manifest.json")
	}
	if !json.Valid(cand.ManifestBytes) || (cand.MetadataBytes != nil && !json.Valid(cand.MetadataBytes)) {
		return nil, errors.New("agent: candidate members are not valid JSON")
	}
	seq, sha, err := envelopeSeqAndHash(cand.ManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("agent: candidate manifest: %w", err)
	}
	cand.PublicationSeq = seq
	cand.CanonicalSHA256 = sha
	return cand, nil
}

// envelopeSeqAndHash accepts either a bare manifest or a
// {manifest, signature} envelope and returns the publication_seq and
// the SHA-256 of the JCS-canonical inner-manifest bytes.
func envelopeSeqAndHash(raw []byte) (uint64, string, error) {
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return 0, "", fmt.Errorf("decode: %w", err)
	}
	inner := probe
	if m, ok := probe["manifest"].(map[string]any); ok {
		inner = m
	}
	canon, err := canonical.Bytes(inner)
	if err != nil {
		return 0, "", fmt.Errorf("canonicalize: %w", err)
	}
	var seq uint64
	switch v := inner["publication_seq"].(type) {
	case float64:
		if v >= 0 {
			seq = uint64(v)
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n >= 0 {
			seq = uint64(n)
		}
	}
	return seq, sha256Hex(canon), nil
}

// retryBackoff returns the exponential push backoff for attempt n
// (0-based): 15s, 30s, 1m, … capped at 10 minutes (§8). Jitter is
// the caller's business.
func retryBackoff(attempt int) time.Duration {
	d := 15 * time.Second << uint(attempt)
	if d > 10*time.Minute || d <= 0 {
		return 10 * time.Minute
	}
	return d
}
