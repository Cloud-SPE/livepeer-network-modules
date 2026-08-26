package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/credentialstore"
)

// Credential-store admin surface (protocols/broker-admin.md §5) and the
// attach-auth hook that consults the store before the legacy
// config-string check.

const maxAdminBody = 1 << 20

// adminError writes the broker-admin §1 error body.
func adminError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Livepeer-Error", code)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": message})
}

func adminJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeAdminBody decodes a JSON body rejecting unknown fields
// (broker-admin §1: silently ignored operator input is how a price ends
// up unset). Reports the offending field name in the error.
func decodeAdminBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxAdminBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if err == io.EOF {
			return true
		}
		msg := err.Error()
		if strings.Contains(msg, "unknown field") {
			adminError(w, http.StatusBadRequest, "unknown_field", msg)
		} else {
			adminError(w, http.StatusBadRequest, "invalid_request", msg)
		}
		return false
	}
	return true
}

// requireCredentialStore is requireAdminAuth plus "a store exists".
func (s *Server) requireCredentialStore(w http.ResponseWriter, r *http.Request) *credentialstore.Store {
	if !s.requireAdminAuth(w, r) {
		return nil
	}
	if s.credentialStore == nil {
		adminError(w, http.StatusNotFound, "credential_store_disabled", "credential_store is not configured")
		return nil
	}
	return s.credentialStore
}

// --- attach-side ---------------------------------------------------------

// authenticateAttachCredential resolves a Bearer authorization against
// the credential store. Nil when there is no store, no bearer, or the
// store rejects it — the caller then falls through to the legacy check.
func (s *Server) authenticateAttachCredential(authz string) *credentialstore.Record {
	if s.credentialStore == nil {
		return nil
	}
	token, ok := strings.CutPrefix(strings.TrimSpace(authz), "Bearer ")
	if !ok {
		return nil
	}
	rec, err := s.credentialStore.Authenticate(token)
	if err != nil {
		return nil
	}
	return rec
}

// trackAttachedHost remembers a connection under its enrollment's host
// id so a revoke can close it. Returns the untrack func. A connection
// that did not authenticate through the store is not tracked.
func (s *Server) trackAttachedHost(authz string, conn io.Closer) func() {
	rec := s.authenticateAttachCredential(authz)
	if rec == nil {
		return func() {}
	}
	s.attachedMu.Lock()
	s.attachedHosts[rec.HostID] = append(s.attachedHosts[rec.HostID], conn)
	s.attachedMu.Unlock()
	return func() {
		s.attachedMu.Lock()
		defer s.attachedMu.Unlock()
		conns := s.attachedHosts[rec.HostID]
		for i, c := range conns {
			if c == conn {
				s.attachedHosts[rec.HostID] = append(conns[:i], conns[i+1:]...)
				break
			}
		}
		if len(s.attachedHosts[rec.HostID]) == 0 {
			delete(s.attachedHosts, rec.HostID)
		}
	}
}

// killHost closes every tracked connection for a host and returns how
// many it closed.
func (s *Server) killHost(hostID string) int {
	s.attachedMu.Lock()
	conns := s.attachedHosts[hostID]
	delete(s.attachedHosts, hostID)
	s.attachedMu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	return len(conns)
}

// --- wire shapes ---------------------------------------------------------

type credentialView struct {
	CredentialID     string        `json:"credential_id"`
	HostID           string        `json:"host_id"`
	Label            string        `json:"label,omitempty"`
	MemberEthAddress string        `json:"member_eth_address,omitempty"`
	Kind             string        `json:"kind"`
	State            string        `json:"state"`
	Source           string        `json:"source"`
	IssuedAt         time.Time     `json:"issued_at"`
	ExpiresAt        time.Time     `json:"expires_at"`
	LastUsedAt       *time.Time    `json:"last_used_at,omitempty"`
	RevokedAt        *time.Time    `json:"revoked_at,omitempty"`
	RevokeReason     string        `json:"revoke_reason,omitempty"`
	Rotation         *rotationView `json:"rotation"`
	SyncRevision     string        `json:"sync_revision,omitempty"`
}

type rotationView struct {
	PreviousExpiresAt time.Time `json:"previous_expires_at"`
}

func viewOf(rec credentialstore.Record) credentialView {
	v := credentialView{
		CredentialID: rec.CredentialID, HostID: rec.HostID, Label: rec.Label,
		MemberEthAddress: rec.MemberEthAddress, Kind: rec.Kind, State: rec.State, Source: rec.Source,
		IssuedAt: rec.IssuedAt, ExpiresAt: rec.ExpiresAt, RevokeReason: rec.RevokeReason, SyncRevision: rec.SyncRevision,
	}
	if !rec.LastUsedAt.IsZero() {
		t := rec.LastUsedAt
		v.LastUsedAt = &t
	}
	if !rec.RevokedAt.IsZero() {
		t := rec.RevokedAt
		v.RevokedAt = &t
	}
	if rec.Rotation != nil {
		v.Rotation = &rotationView{PreviousExpiresAt: rec.Rotation.PreviousExpiresAt}
	}
	return v
}

type enrollResponse struct {
	CredentialID string            `json:"credential_id"`
	HostID       string            `json:"host_id"`
	Credential   map[string]string `json:"credential"`
	ExpiresAt    time.Time         `json:"expires_at"`
	Bundle       enrollBundle      `json:"bundle"`
}

type enrollBundle struct {
	BrokerURLs       map[string]string `json:"broker_urls"`
	BrokerEthAddress string            `json:"broker_eth_address"`
	ContractVersion  string            `json:"contract_version"`
}

func (s *Server) enrollResponseFor(res *credentialstore.EnrollResult) enrollResponse {
	cfg := s.currentConfig()
	urls := map[string]string{}
	if base := strings.TrimRight(cfg.ExternalBaseURL, "/"); base != "" {
		urls["ws"] = strings.Replace(base, "http", "ws", 1) + "/internal/v1/worker/session"
	}
	if q := strings.TrimSpace(cfg.Listen.WorkerQUIC); q != "" {
		urls["quic"] = q
	}
	return enrollResponse{
		CredentialID: res.Record.CredentialID,
		HostID:       res.Record.HostID,
		Credential:   map[string]string{"kind": res.Record.Kind, "token": res.Token},
		ExpiresAt:    res.Record.ExpiresAt,
		Bundle: enrollBundle{
			BrokerURLs:       urls,
			BrokerEthAddress: cfg.Identity.OrchEthAddress,
			ContractVersion:  "1.0",
		},
	}
}

// --- handlers ------------------------------------------------------------

func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	st := s.requireCredentialStore(w, r)
	if st == nil {
		return
	}
	var body struct {
		HostID           string `json:"host_id"`
		Label            string `json:"label"`
		ExpiresInSeconds int    `json:"expires_in_seconds"`
		Kind             string `json:"kind"`
		MemberEthAddress string `json:"member_eth_address"`
	}
	if !decodeAdminBody(w, r, &body) {
		return
	}
	if body.ExpiresInSeconds < 0 {
		adminError(w, http.StatusBadRequest, "invalid_request", "expires_in_seconds must be >= 0")
		return
	}
	res, err := st.Enroll(credentialstore.EnrollRequest{
		HostID: body.HostID, Label: body.Label, Kind: body.Kind,
		ExpiresIn:        time.Duration(body.ExpiresInSeconds) * time.Second,
		MemberEthAddress: body.MemberEthAddress,
		RequestID:        strings.TrimSpace(r.Header.Get("Livepeer-Request-Id")),
	})
	switch {
	case errors.Is(err, credentialstore.ErrHostTaken):
		adminError(w, http.StatusConflict, "host_id_taken", err.Error())
		return
	case errors.Is(err, credentialstore.ErrKindUnsupported):
		adminError(w, http.StatusBadRequest, "credential_kind_unsupported", err.Error())
		return
	case err != nil:
		adminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	adminJSON(w, http.StatusCreated, s.enrollResponseFor(res))
}

func (s *Server) handleCredentialsList(w http.ResponseWriter, r *http.Request) {
	st := s.requireCredentialStore(w, r)
	if st == nil {
		return
	}
	recs, err := st.List()
	if err != nil {
		adminError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	views := make([]credentialView, 0, len(recs))
	for _, rec := range recs {
		views = append(views, viewOf(rec))
	}
	adminJSON(w, http.StatusOK, map[string]any{"credentials": views, "next_cursor": nil})
}

func (s *Server) handleCredentialGet(w http.ResponseWriter, r *http.Request) {
	st := s.requireCredentialStore(w, r)
	if st == nil {
		return
	}
	rec, err := st.Get(r.PathValue("credential_id"))
	if errors.Is(err, credentialstore.ErrNotFound) {
		adminError(w, http.StatusNotFound, "credential_not_found", "no such credential")
		return
	}
	if err != nil {
		adminError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	adminJSON(w, http.StatusOK, viewOf(*rec))
}

func (s *Server) handleCredentialRotate(w http.ResponseWriter, r *http.Request) {
	st := s.requireCredentialStore(w, r)
	if st == nil {
		return
	}
	var body struct {
		GraceSeconds int `json:"grace_seconds"`
	}
	if !decodeAdminBody(w, r, &body) {
		return
	}
	res, err := st.Rotate(r.PathValue("credential_id"), time.Duration(body.GraceSeconds)*time.Second)
	switch {
	case errors.Is(err, credentialstore.ErrNotFound):
		adminError(w, http.StatusNotFound, "credential_not_found", "no such credential")
		return
	case errors.Is(err, credentialstore.ErrRevoked):
		adminError(w, http.StatusConflict, "credential_revoked", "credential is revoked")
		return
	case err != nil:
		adminError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	adminJSON(w, http.StatusCreated, s.enrollResponseFor(res))
}

func (s *Server) handleCredentialRevoke(w http.ResponseWriter, r *http.Request) {
	st := s.requireCredentialStore(w, r)
	if st == nil {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeAdminBody(w, r, &body) {
		return
	}
	rec, err := st.Revoke(r.PathValue("credential_id"), body.Reason)
	if errors.Is(err, credentialstore.ErrNotFound) {
		adminError(w, http.StatusNotFound, "credential_not_found", "no such credential")
		return
	}
	if err != nil {
		adminError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	closed := s.killHost(rec.HostID)
	adminJSON(w, http.StatusOK, map[string]any{
		"credential_id": rec.CredentialID, "state": rec.State, "connections_closed": closed,
	})
}

func (s *Server) handleCredentialsSync(w http.ResponseWriter, r *http.Request) {
	st := s.requireCredentialStore(w, r)
	if st == nil {
		return
	}
	var body struct {
		Revision    string                      `json:"revision"`
		Credentials []credentialstore.SyncEntry `json:"credentials"`
	}
	if !decodeAdminBody(w, r, &body) {
		return
	}
	revokedHosts, err := st.SyncReplace(body.Revision, body.Credentials)
	if err != nil {
		adminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	closed := 0
	for _, h := range revokedHosts {
		closed += s.killHost(h)
	}
	adminJSON(w, http.StatusOK, map[string]any{
		"revision": body.Revision, "applied": true, "credentials": len(body.Credentials),
		"revoked_hosts": revokedHosts, "connections_closed": closed,
	})
}
