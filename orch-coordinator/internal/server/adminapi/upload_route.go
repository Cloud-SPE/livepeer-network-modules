package adminapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

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
	s.mux.HandleFunc("POST /admin/signed-manifest", func(w http.ResponseWriter, r *http.Request) {
		body, uploader, err := readUpload(r)
		if err != nil {
			httpJSON(w, http.StatusBadRequest, map[string]any{
				"outcome": "schema_invalid",
				"error":   err.Error(),
			})
			return
		}
		res, err := rec.Receive(body, uploader)
		if err != nil {
			var ve *receive.VerifyError
			if errors.As(err, &ve) {
				httpJSON(w, statusForOutcome(ve.Code), map[string]any{
					"outcome": string(ve.Code),
					"error":   ve.Msg,
				})
				return
			}
			httpJSON(w, http.StatusInternalServerError, map[string]any{
				"outcome": "internal_error",
				"error":   err.Error(),
			})
			return
		}
		httpJSON(w, http.StatusOK, map[string]any{
			"outcome":          "accepted",
			"publication_seq":  res.PublicationSeq,
			"manifest_sha256":  res.ManifestSHA256,
			"signature_sha256": res.SignatureSHA256,
		})
	})
}

// readUpload extracts the manifest bytes from a multipart upload OR a
// raw JSON body. Operators with `curl -F` use multipart; programmatic
// uploaders may POST `application/json` directly.
func readUpload(r *http.Request) ([]byte, string, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, MaxUploadBytes)
	uploader := r.RemoteAddr
	if h := r.Header.Get("X-Uploader"); h != "" {
		uploader = h
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
