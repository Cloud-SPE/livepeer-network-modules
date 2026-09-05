package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/offers"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/registry"
)

// Offer admin surface (protocols/broker-admin.md §4).

type offerCandidateView struct {
	ShapeHash  string                  `json:"shape_hash"`
	FirstSeen  string                  `json:"first_seen"`
	Runners    []offers.FrozenBy       `json:"runners"`
	Diff       []runnerattach.Reason   `json:"diff"`
	Projection runnerattach.Projection `json:"projection"`
}

type offerView struct {
	offers.View
	Candidates      []offerCandidateView `json:"candidates"`
	AdvertisedTuple any                  `json:"advertised_tuple,omitempty"`
}

func (s *Server) offerViewOf(v offers.View) offerView {
	out := offerView{View: v, Candidates: []offerCandidateView{}}
	for _, c := range v.Candidates {
		out.Candidates = append(out.Candidates, offerCandidateView{
			ShapeHash: c.ShapeHash, FirstSeen: c.FirstSeen.UTC().Format("2006-01-02T15:04:05Z07:00"),
			Runners: c.Runners, Diff: c.Diff, Projection: c.Projection,
		})
	}
	if v.Advertised {
		shape := v.Pending
		if shape == nil {
			shape = v.Frozen
		}
		if t := registry.OfferTuple(v.Operator, offerShape(shape)); t != nil {
			out.AdvertisedTuple = t
		}
	}
	return out
}

func offerShape(f *offers.Frozen) registry.FrozenShape {
	return registry.FrozenShape{
		Projection:               f.Projection,
		SessionParamsSchema:      f.SessionParamsSchema,
		HeartbeatIntervalSeconds: f.HeartbeatIntervalSeconds,
	}
}

func (s *Server) requireOffersEngine(w http.ResponseWriter, r *http.Request) *offers.Engine {
	if !s.requireAdminAuth(w, r) {
		return nil
	}
	return s.offersEngine
}

func (s *Server) handleOffersList(w http.ResponseWriter, r *http.Request) {
	e := s.requireOffersEngine(w, r)
	if e == nil {
		return
	}
	views := []offerView{}
	for _, v := range e.Views() {
		views = append(views, s.offerViewOf(v))
	}
	adminJSON(w, http.StatusOK, map[string]any{"offers": views, "offers_revision": e.Revision(), "next_cursor": nil})
}

func (s *Server) handleOfferGet(w http.ResponseWriter, r *http.Request) {
	e := s.requireOffersEngine(w, r)
	if e == nil {
		return
	}
	v, ok := e.ViewOf(r.PathValue("offering_id"))
	if !ok {
		adminError(w, http.StatusNotFound, "offer_not_found", "no such offering_id")
		return
	}
	adminJSON(w, http.StatusOK, s.offerViewOf(v))
}

func (s *Server) handleOffersPut(w http.ResponseWriter, r *http.Request) {
	e := s.requireOffersEngine(w, r)
	if e == nil {
		return
	}
	// Source check first: a file-sourced broker answers 409 regardless
	// of body validity (broker-admin §4.2).
	if !e.SourceIsAdmin() {
		adminError(w, http.StatusConflict, "offers_source_is_file", offers.ErrSourceIsFile.Error())
		return
	}
	var body struct {
		Revision string          `json:"revision"`
		Offers   json.RawMessage `json:"offers"`
	}
	if !decodeAdminBody(w, r, &body) {
		return
	}
	// The push body is the same offers[] grammar the file accepts,
	// validated in full before anything changes (broker-admin §4.2).
	var list []config.Offer
	var rdr io.Reader = bytes.NewReader(body.Offers)
	dec := json.NewDecoder(rdr)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&list); err != nil {
		adminError(w, http.StatusBadRequest, "offer_invalid", err.Error())
		return
	}
	if len(list) > 0 {
		probe := &config.Config{
			Identity:        s.currentConfig().Identity,
			ExternalBaseURL: s.currentConfig().ExternalBaseURL,
			SessionStore:    s.currentConfig().SessionStore,
			AdminAuth:       s.currentConfig().AdminAuth,
			OffersSource:    config.OffersSourceFile, // validate the grammar as if file-sourced: the push IS the source
			Offers:          list,
		}
		if err := probe.Validate(); err != nil {
			adminError(w, http.StatusBadRequest, "offer_invalid", err.Error())
			return
		}
	}
	changed, err := e.Push(body.Revision, list)
	if err != nil {
		adminError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if changed == nil {
		changed = []string{}
	}
	adminJSON(w, http.StatusOK, map[string]any{
		"revision": body.Revision, "applied": true, "offers": len(list), "changed": changed,
	})
}

func (s *Server) handleOfferAcceptShape(w http.ResponseWriter, r *http.Request) {
	e := s.requireOffersEngine(w, r)
	if e == nil {
		return
	}
	var body struct {
		ShapeHash string `json:"shape_hash"`
	}
	if !decodeAdminBody(w, r, &body) {
		return
	}
	id := r.PathValue("offering_id")
	elig, inelig, err := e.AcceptShape(id, body.ShapeHash)
	switch {
	case errors.Is(err, offers.ErrNotFound):
		adminError(w, http.StatusNotFound, "offer_not_found", "no such offering_id")
		return
	case errors.Is(err, offers.ErrNotCandidate):
		adminError(w, http.StatusConflict, "shape_not_candidate", "shape_hash is not a current candidate")
		return
	case err != nil:
		adminError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	adminJSON(w, http.StatusAccepted, map[string]any{
		"offering_id": id, "state": offers.OfferSuperseding, "shape_hash": body.ShapeHash,
		"eligible_now": elig, "ineligible_now": inelig,
	})
}

func (s *Server) handleOfferConfirmPublished(w http.ResponseWriter, r *http.Request) {
	e := s.requireOffersEngine(w, r)
	if e == nil {
		return
	}
	var body struct {
		ShapeHash string `json:"shape_hash"`
	}
	if !decodeAdminBody(w, r, &body) {
		return
	}
	id := r.PathValue("offering_id")
	if err := e.ConfirmPublished(id, body.ShapeHash); err != nil {
		if errors.Is(err, offers.ErrNotFound) {
			adminError(w, http.StatusNotFound, "offer_not_found", "no such offering_id")
			return
		}
		adminError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	v, _ := e.ViewOf(id)
	adminJSON(w, http.StatusOK, map[string]any{"offering_id": id, "state": v.State})
}

func (s *Server) handleOfferDisable(w http.ResponseWriter, r *http.Request) {
	s.setOfferDisabled(w, r, true)
}

func (s *Server) handleOfferEnable(w http.ResponseWriter, r *http.Request) {
	s.setOfferDisabled(w, r, false)
}

func (s *Server) setOfferDisabled(w http.ResponseWriter, r *http.Request, disabled bool) {
	e := s.requireOffersEngine(w, r)
	if e == nil {
		return
	}
	id := r.PathValue("offering_id")
	if err := e.SetDisabled(id, disabled); err != nil {
		adminError(w, http.StatusNotFound, "offer_not_found", "no such offering_id")
		return
	}
	v, _ := e.ViewOf(id)
	adminJSON(w, http.StatusOK, map[string]any{"offering_id": id, "state": v.State})
}
