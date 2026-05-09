package adminapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/receive"
)

// MaxUploadBytes caps the multipart-upload body size. The signed
// manifest is small (a few KiB at most for any realistic capability
// roster); 1 MiB is a generous ceiling that bounds memory.
const MaxUploadBytes = 1 << 20

// UploadRoutes wires the signed-manifest upload endpoint onto the
// admin mux.
//
//	POST /admin/signed-manifest    multipart/form-data with field "manifest"
func (s *Server) UploadRoutes(rec *receive.Service) {
	s.mux.HandleFunc("POST /admin/signed-manifest", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		res, outcome, msg, status := receiveUpload(rec, r)
		if status != http.StatusOK || res == nil {
			httpJSON(w, status, map[string]any{
				"outcome": string(outcome),
				"error":   msg,
			})
			return
		}
		httpJSON(w, http.StatusOK, map[string]any{
			"outcome":          "accepted",
			"publication_seq":  res.PublicationSeq,
			"manifest_sha256":  res.ManifestSHA256,
			"signature_sha256": res.SignatureSHA256,
		})
	}))
}

func receiveUpload(rec *receive.Service, r *http.Request) (*receive.Result, audit.Outcome, string, int) {
	body, uploader, err := readUpload(r)
	if err != nil {
		return nil, audit.OutcomeSchemaInvalid, err.Error(), http.StatusBadRequest
	}
	res, err := rec.ReceiveFrom(body, receive.UploaderIdentity{
		Name:  uploader,
		Actor: actorFromRequest(r),
	})
	if err != nil {
		var ve *receive.VerifyError
		if errors.As(err, &ve) {
			return nil, ve.Code, ve.Msg, statusForOutcome(ve.Code)
		}
		return nil, audit.OutcomePublishFailed, err.Error(), http.StatusInternalServerError
	}
	return res, audit.OutcomeAccepted, "", http.StatusOK
}

// readUpload extracts the manifest bytes from a multipart upload OR a
// raw JSON body. Operators with `curl -F` use multipart; programmatic
// uploaders may POST `application/json` directly.
func readUpload(r *http.Request) ([]byte, string, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, MaxUploadBytes)
	uploader := strings.TrimSpace(actorFromRequest(r))
	if uploader == "" {
		uploader = r.RemoteAddr
		if h := r.Header.Get("X-Uploader"); h != "" {
			uploader = h
		}
	}
	ct := r.Header.Get("Content-Type")
	if len(ct) >= 19 && ct[:19] == "multipart/form-data" {
		if err := r.ParseMultipartForm(MaxUploadBytes); err != nil {
			return nil, uploader, fmt.Errorf("parse multipart: %w", err)
		}
		f, _, err := r.FormFile("manifest")
		if err != nil {
			return nil, uploader, fmt.Errorf("missing form-field 'manifest': %w", err)
		}
		defer f.Close()
		body, err := io.ReadAll(f)
		if err != nil {
			return nil, uploader, fmt.Errorf("read upload: %w", err)
		}
		return body, uploader, nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, uploader, fmt.Errorf("read body: %w", err)
	}
	return body, uploader, nil
}

func statusForOutcome(o audit.Outcome) int {
	switch o {
	case audit.OutcomeAccepted:
		return http.StatusOK
	case audit.OutcomeSchemaInvalid:
		return http.StatusBadRequest
	case audit.OutcomeSigInvalid, audit.OutcomeIdentityMismatch:
		return http.StatusUnauthorized
	case audit.OutcomeDriftRejected, audit.OutcomeRollbackRejected, audit.OutcomeWindowInvalid:
		return http.StatusConflict
	case audit.OutcomePublishFailed:
		return http.StatusInternalServerError
	default:
		return http.StatusBadRequest
	}
}

func httpJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
