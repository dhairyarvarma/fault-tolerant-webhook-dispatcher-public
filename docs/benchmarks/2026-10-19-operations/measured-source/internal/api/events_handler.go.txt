package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

type createEventRequest struct {
	Payload json.RawMessage `json:"payload"`
}

type eventResponse struct {
	EventID       string     `json:"event_id"`
	TargetID      string     `json:"target_id"`
	Status        string     `json:"status"`
	AttemptCount  int        `json:"attempt_count"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

func toEventResponse(e store.Event) eventResponse {
	return eventResponse{
		EventID:       e.ID,
		TargetID:      e.TargetID,
		Status:        e.Status,
		AttemptCount:  e.AttemptCount,
		NextAttemptAt: e.NextAttemptAt,
		CreatedAt:     e.CreatedAt,
		UpdatedAt:     e.UpdatedAt,
		CompletedAt:   e.CompletedAt,
	}
}

func (s *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	targetID := r.PathValue("target_id")
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		s.metrics.EventRejected()
		writeError(w, r, http.StatusBadRequest, CodeValidationError, "Content-Type must be application/json")
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		s.metrics.EventRejected()
		writeError(w, r, http.StatusBadRequest, CodeValidationError, "Idempotency-Key header is required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.payloadMaxBytes)
	var req createEventRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			s.metrics.EventRejected()
			writeError(w, r, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request payload exceeds the configured size limit")
			return
		}
		s.metrics.EventRejected()
		writeError(w, r, http.StatusBadRequest, CodeValidationError, "request body must be valid JSON")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		s.metrics.EventRejected()
		writeError(w, r, http.StatusBadRequest, CodeValidationError, "request body must contain one JSON value")
		return
	}

	event, created, err := s.events.Create(r.Context(), targetID, idempotencyKey, req.Payload)
	if err != nil {
		s.metrics.EventRejected()
		s.writeServiceError(w, r, err)
		return
	}

	if !created {
		s.metrics.EventRejected()
		writeJSON(w, http.StatusConflict, struct {
			Error   errorBody `json:"error"`
			EventID string    `json:"event_id"`
		}{
			Error: errorBody{
				Code:      CodeConflict,
				Message:   "an event already exists for this target and Idempotency-Key",
				RequestID: requestIDFromContext(r.Context()),
			},
			EventID: event.ID,
		})
		return
	}

	s.metrics.EventCreated()
	writeJSON(w, http.StatusCreated, toEventResponse(event))
}

func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("event_id")

	event, err := s.events.Get(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, toEventResponse(event))
}

type attemptResponse struct {
	AttemptID     string     `json:"attempt_id"`
	AttemptNumber int        `json:"attempt_number"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	Outcome       string     `json:"outcome"`
	HTTPStatus    *int       `json:"http_status,omitempty"`
	DurationMS    *int       `json:"duration_ms,omitempty"`
	ErrorClass    *string    `json:"error_class,omitempty"`
}

const defaultAttemptsLimit = 50

func (s *Server) handleListEventAttempts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("event_id")

	attempts, err := s.events.ListAttempts(r.Context(), id, defaultAttemptsLimit)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}

	out := make([]attemptResponse, 0, len(attempts))
	for _, a := range attempts {
		out = append(out, attemptResponse{
			AttemptID:     a.ID,
			AttemptNumber: a.AttemptNumber,
			StartedAt:     a.StartedAt,
			FinishedAt:    a.FinishedAt,
			Outcome:       a.Outcome,
			HTTPStatus:    a.HTTPStatus,
			DurationMS:    a.DurationMS,
			ErrorClass:    a.ErrorClass,
		})
	}

	writeJSON(w, http.StatusOK, struct {
		EventID  string            `json:"event_id"`
		Attempts []attemptResponse `json:"attempts"`
	}{EventID: id, Attempts: out})
}

// handleReplayEvent requests an explicit retry/replay of an eligible (DLQ)
// event, per API_SPEC.md.
func (s *Server) handleReplayEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("event_id")

	event, err := s.events.Replay(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, toEventResponse(event))
}
