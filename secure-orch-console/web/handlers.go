package web

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/canonical"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/diff"
)

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	last, err := loadLastSigned(s.cfg.LastSignedPath)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "load last-signed", err)
		return
	}
	view := indexView{
		SignerAddress:  s.signer.Address().String(),
		LastSignedPath: s.cfg.LastSignedPath,
		HasLastSigned:  last != nil,
	}
	if last != nil {
		view.LastSignedSummary = summarizeEnvelope(last)
	}
	s.mu.Lock()
	cand := s.candidate
	s.mu.Unlock()
	if cand != nil {
		res, err := diff.Compute(last, cand.bytes)
		if err != nil {
			view.CandidateError = err.Error()
		} else {
			view.Candidate = &candidateView{
				LoadedAt:   cand.loadedAt.Format(time.RFC3339),
				SourceName: cand.sourceName,
				CanonHash:  cand.canonHash,
				Diff:       res,
			}
		}
	}
	if appendErr := s.audit.Append(audit.Event{
		Kind:       audit.KindViewDiff,
		EthAddress: s.signer.Address().String(),
	}); appendErr != nil {
		s.logger.Warn("audit append failed", "err", appendErr)
	}
	if err := s.templates.render(w, "index.html", view); err != nil {
		s.logger.Warn("render index", "err", err)
	}
}

func (s *Server) handleCandidate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload)
	if err := r.ParseMultipartForm(s.maxUpload); err != nil {
		s.fail(w, r, http.StatusBadRequest, "parse multipart", err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("candidate")
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "candidate file", err)
		return
	}
	defer file.Close()
	manifestBytes, err := extractManifestJSON(file, header.Filename)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "extract manifest.json", err)
		return
	}
	if !json.Valid(manifestBytes) {
		s.fail(w, r, http.StatusBadRequest, "manifest.json", errors.New("not valid JSON"))
		return
	}
	canon, err := canonicalManifestBytes(manifestBytes)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "canonicalize manifest", err)
		return
	}
	stash := &stashedCandidate{
		bytes:      manifestBytes,
		loadedAt:   time.Now().UTC(),
		canonHash:  canonical.SHA256Hex(canon),
		sourceName: filepath.Base(header.Filename),
	}
	s.mu.Lock()
	s.candidate = stash
	s.mu.Unlock()
	if appendErr := s.audit.Append(audit.Event{
		Kind:       audit.KindLoadCandidate,
		EthAddress: s.signer.Address().String(),
		CanonHash:  stash.canonHash,
		Note:       stash.sourceName,
	}); appendErr != nil {
		s.logger.Warn("audit append failed", "err", appendErr)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDiscard(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	had := s.candidate != nil
	s.candidate = nil
	s.mu.Unlock()
	if had {
		if appendErr := s.audit.Append(audit.Event{
			Kind:       audit.KindAbort,
			EthAddress: s.signer.Address().String(),
			Note:       "operator discarded candidate",
		}); appendErr != nil {
			s.logger.Warn("audit append failed", "err", appendErr)
		}
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleSign(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "parse form", err)
		return
	}
	confirm := strings.ToLower(strings.TrimSpace(r.PostForm.Get("confirm_last4")))
	expected := lastFourHex(s.signer.Address().String())
	if confirm == "" || confirm != expected {
		s.audit.Append(audit.Event{
			Kind:       audit.KindAbort,
			EthAddress: s.signer.Address().String(),
			Note:       "confirm gesture failed",
		})
		s.fail(w, r, http.StatusBadRequest, "confirm gesture", fmt.Errorf("last 4 hex chars do not match signer address"))
		return
	}
	s.mu.Lock()
	cand := s.candidate
	s.mu.Unlock()
	if cand == nil {
		s.fail(w, r, http.StatusBadRequest, "sign", errors.New("no candidate loaded"))
		return
	}
	envelope, err := signCandidate(cand.bytes, s.signer)
	if err != nil {
		s.audit.Append(audit.Event{
			Kind:       audit.KindAbort,
			EthAddress: s.signer.Address().String(),
			Note:       "sign failed: " + err.Error(),
		})
		s.fail(w, r, http.StatusInternalServerError, "sign", err)
		return
	}
	canon, err := canonicalManifestBytes(cand.bytes)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "canonicalize", err)
		return
	}
	canonHash := canonical.SHA256Hex(canon)
	seq := publicationSeq(cand.bytes)

	if err := writeLastSignedAtomic(s.cfg.LastSignedPath, envelope); err != nil {
		s.audit.Append(audit.Event{
			Kind:       audit.KindAbort,
			EthAddress: s.signer.Address().String(),
			Note:       "write last-signed failed: " + err.Error(),
		})
		s.fail(w, r, http.StatusInternalServerError, "write last-signed", err)
		return
	}

	signEvent := audit.Event{
		Kind:       audit.KindSign,
		EthAddress: s.signer.Address().String(),
		CanonHash:  canonHash,
	}
	if seq != nil {
		signEvent.Seq = seq
	}
	if appendErr := s.audit.Append(signEvent); appendErr != nil {
		s.logger.Warn("audit append failed", "err", appendErr)
	}
	if appendErr := s.audit.Append(audit.Event{
		Kind:       audit.KindWriteSigned,
		EthAddress: s.signer.Address().String(),
		CanonHash:  canonHash,
		Note:       cand.sourceName,
	}); appendErr != nil {
		s.logger.Warn("audit append failed", "err", appendErr)
	}

	s.mu.Lock()
	s.candidate = nil
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="signed.json"`)
	w.Header().Set("X-Canonical-SHA256", canonHash)
	if _, err := w.Write(envelope); err != nil {
		s.logger.Warn("write signed response", "err", err)
	}
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, what string, err error) {
	s.logger.Warn("handler failed", "what", what, "method", r.Method, "path", r.URL.Path, "err", err)
	http.Error(w, fmt.Sprintf("%s: %s", what, err), code)
}

// extractManifestJSON pulls manifest.json out of a tarball or returns
// the body unchanged if it already looks like raw JSON. The console
// accepts both shapes during operator transition; the runbook
// recommends the tarball form (manifest.json + metadata.json) per
// plan 0018 Q3.
func extractManifestJSON(r io.Reader, filename string) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, 16<<20))
	if err != nil {
		return nil, err
	}
	if looksLikeJSON(buf) {
		return buf, nil
	}
	return readManifestFromTar(buf, filename)
}

func looksLikeJSON(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}

func readManifestFromTar(buf []byte, filename string) ([]byte, error) {
	var reader io.Reader = bytes.NewReader(buf)
	lower := strings.ToLower(filename)
	if strings.HasSuffix(lower, ".gz") || strings.HasSuffix(lower, ".tgz") {
		gz, err := gzip.NewReader(bytes.NewReader(buf))
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		defer gz.Close()
		reader = gz
	}
	tr := tar.NewReader(reader)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("manifest.json not found in archive")
		}
		if err != nil {
			return nil, fmt.Errorf("tar: %w", err)
		}
		if filepath.Base(hdr.Name) == "manifest.json" {
			return io.ReadAll(io.LimitReader(tr, 16<<20))
		}
	}
}
