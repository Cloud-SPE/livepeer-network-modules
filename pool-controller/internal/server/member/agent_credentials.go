package member

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func registerAgentCredentials(mux *http.ServeMux, deps Deps) {
	mux.HandleFunc("GET /member/v1/enrollments/{id}/agent-credentials", func(w http.ResponseWriter, r *http.Request) {
		enrollment, ok := authorizeEnrollment(deps, r)
		if !ok {
			http.Error(w, "agent token required", 401)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{"pool_id": deps.Repo.PoolID(), "enrollment_id": enrollment.ID, "attach_credential": enrollment.BrokerSessionCredential, "credential_generation": enrollment.CredentialGeneration})
	})
	for _, action := range []string{"agent-rotation", "agent-rotation-ack"} {
		mux.HandleFunc("POST /member/v1/enrollments/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token == r.Header.Get("Authorization") || token == "" {
				http.Error(w, "agent token required", 401)
				return
			}
			var input struct {
				RequestID string `json:"request_id"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
				http.Error(w, "invalid rotation request", 400)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			if action == "agent-rotation-ack" {
				if err := deps.Repo.AckAgentRotation(r.PathValue("id"), token, input.RequestID); err != nil {
					http.Error(w, "rotation acknowledgement rejected", 401)
					return
				}
				w.WriteHeader(204)
				return
			}
			pair, err := deps.Repo.RotateAgentCredentials(r.PathValue("id"), token, input.RequestID)
			if err != nil {
				http.Error(w, "agent rotation rejected", 401)
				return
			}
			if deps.RefreshTerms != nil {
				if err := deps.RefreshTerms(); err != nil {
					http.Error(w, "rotation saved; broker synchronization pending; retry the same request proof", 503)
					return
				}
			}
			writeJSON(w, 200, pair)
		})
	}
}
