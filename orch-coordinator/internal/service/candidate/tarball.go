package candidate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

// PackTarball wraps the candidate as a tar.gz with two members:
// manifest.json (signed bytes) and metadata.json (operator-only
// sidecar). The byte-identical guarantee on manifest.json is what the
// cold key signs; metadata.json carries provenance that must NOT
// enter the signed bytes.
//
// The tar header timestamps are pinned to the candidate's issued_at
// so the tarball is byte-stable for a given candidate.
func PackTarball(c *types.Candidate) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("candidate: nil")
	}
	metaBytes, err := MarshalMetadata(c.Metadata)
	if err != nil {
		return nil, fmt.Errorf("metadata marshal: %w", err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	pinned := c.Metadata.CandidateTimestamp
	if pinned.IsZero() {
		pinned = time.Now().UTC()
	}

	if err := writeMember(tw, "manifest.json", c.ManifestBytes, pinned); err != nil {
		return nil, err
	}
	if err := writeMember(tw, "metadata.json", metaBytes, pinned); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeMember(tw *tar.Writer, name string, body []byte, t time.Time) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o600,
		Size:    int64(len(body)),
		ModTime: t.UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tar header %s: %w", name, err)
	}
	if _, err := tw.Write(body); err != nil {
		return fmt.Errorf("tar body %s: %w", name, err)
	}
	return nil
}
