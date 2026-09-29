package api

import (
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/service"
	"net/http"
)

func (s *Server) listHandovers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.plant.ListHandovers()})
}

func (s *Server) latestHandover(w http.ResponseWriter, r *http.Request) {
	handover, ok := s.plant.LatestHandover()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no handover yet"})
		return
	}
	writeJSON(w, http.StatusOK, handover)
}

func (s *Server) getHandover(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid handover id"})
		return
	}
	handover, ok := s.plant.GetHandover(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "handover not found"})
		return
	}
	writeJSON(w, http.StatusOK, handover)
}

func (s *Server) createHandover(w http.ResponseWriter, r *http.Request) {
	var input handoverRequest
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if input.OutgoingOperator == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "outgoing_operator is required"})
		return
	}
	handover, err := s.plant.OpenHandover(input.OutgoingOperator)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, handover)
}

func (s *Server) signHandover(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid handover id"})
		return
	}
	var input signHandoverRequest
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if input.Successor == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "successor is required"})
		return
	}
	handover, err := s.plant.SignHandover(id, input.Successor)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, service.ErrHandoverMissing) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, handover)
}

func (s *Server) appendHandoverEvent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid handover id"})
		return
	}
	var input handoverEventRequest
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if input.Kind == "" || input.Message == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kind and message are required"})
		return
	}
	handover, err := s.plant.AppendHandoverNote(id, input.Kind, input.Message)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, service.ErrHandoverMissing) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, handover)
}
