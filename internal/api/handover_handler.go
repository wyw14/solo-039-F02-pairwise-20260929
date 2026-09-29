package api

import (
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"net/http"
)

func (s *Server) createHandover(w http.ResponseWriter, r *http.Request) {
	var input handoverRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	h := s.plant.BeginHandover(input.Operator)
	writeJSON(w, http.StatusCreated, h)
}

func (s *Server) listHandovers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.plant.Handovers.List()})
}

func (s *Server) getHandover(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid handover id"})
		return
	}
	h, ok := s.plant.Handovers.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "handover not found"})
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) signHandover(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid handover id"})
		return
	}
	var input handoverSignRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	h, err := s.plant.SignHandover(id, input.Receiver)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, h)
}
