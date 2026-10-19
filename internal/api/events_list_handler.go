package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/service"
)

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	status := strings.ToUpper(strings.TrimSpace(query.Get("status")))

	limit := 0
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, CodeValidationError, "limit must be an integer")
			return
		}
		limit = n
	}

	events, err := s.events.ListEvents(r.Context(), status, limit)
	if err != nil {
		var validationErr *service.ValidationError
		if errors.As(err, &validationErr) {
			writeError(w, r, http.StatusUnprocessableEntity, CodeValidationError, validationErr.Message)
			return
		}
		s.writeServiceError(w, r, err)
		return
	}

	out := make([]eventResponse, 0, len(events))
	for _, ev := range events {
		out = append(out, toEventResponse(ev))
	}
	type eventsListResponse struct {
		Events []eventResponse `json:"events"`
	}
	writeJSON(w, http.StatusOK, eventsListResponse{Events: out})
}
