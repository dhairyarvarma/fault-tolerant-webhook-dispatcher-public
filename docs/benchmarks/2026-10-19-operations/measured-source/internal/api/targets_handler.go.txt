package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/service"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

type createTargetRequest struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Secret string `json:"secret"`
}

type targetResponse struct {
	TargetID     string    `json:"target_id"`
	Name         string    `json:"name"`
	URL          string    `json:"url"`
	Enabled      bool      `json:"enabled"`
	CircuitState string    `json:"circuit_state"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func toTargetResponse(t store.Target) targetResponse {
	return targetResponse{
		TargetID:     t.ID,
		Name:         t.Name,
		URL:          t.URL,
		Enabled:      t.Enabled,
		CircuitState: t.CircuitState,
		CreatedAt:    t.CreatedAt,
		UpdatedAt:    t.UpdatedAt,
	}
}

func (s *Server) handleCreateTarget(w http.ResponseWriter, r *http.Request) {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, r, 400, CodeValidationError, "Content-Type must be application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.payloadMaxBytes)
	decoder := json.NewDecoder(r.Body)
	var req createTargetRequest
	if err := decoder.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, r, 413, CodePayloadTooLarge, "request payload exceeds the configured size limit")
			return
		}
		writeError(w, r, http.StatusBadRequest, CodeValidationError, "request body must be valid JSON")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, r, 400, CodeValidationError, "request body must contain one JSON value")
		return
	}

	target, err := s.targets.Create(r.Context(), req.Name, req.URL, req.Secret)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, toTargetResponse(target))
}

func (s *Server) handleGetTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("target_id")

	target, err := s.targets.Get(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, toTargetResponse(target))
}

func (s *Server) handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("target_id")

	if err := s.targets.Disable(r.Context(), id); err != nil {
		s.writeServiceError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeServiceError maps service/store errors to the stable HTTP error
// envelope documented in API_SPEC.md.
func (s *Server) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var validationErr *service.ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeError(w, r, http.StatusUnprocessableEntity, CodeValidationError, validationErr.Message)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, r, http.StatusNotFound, CodeNotFound, "resource not found")
	case errors.Is(err, service.ErrTargetDisabled):
		writeError(w, r, http.StatusForbidden, CodeForbidden, "target is disabled")
	case errors.Is(err, service.ErrNotEligibleForReplay):
		writeError(w, r, http.StatusConflict, CodeConflict, "event is not eligible for replay")
	case errors.Is(err, service.ErrOverloaded):
		// Admission control (DECISIONS.md #28). Retry-After is advisory: the
		// bound clears as workers drain the durable queue.
		w.Header().Set("Retry-After", "5")
		writeError(w, r, http.StatusTooManyRequests, CodeOverloaded,
			"pending event limit reached; retry after some events have been delivered")
	default:
		s.logger.Error("unhandled request error", "error", err, "request_id", requestIDFromContext(r.Context()))
		writeError(w, r, http.StatusInternalServerError, CodeInternalError, "an unexpected error occurred")
	}
}
