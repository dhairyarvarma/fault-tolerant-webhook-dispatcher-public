// Package api implements the HTTP API layer: request validation,
// authentication for non-local deployments, and the target/event/health
// endpoints described in API_SPEC.md.
package api

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/observability"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/service"
)

// Server holds the HTTP layer's dependencies. Business logic lives in the
// service package; handlers only translate HTTP <-> service calls.
type Server struct {
	targets         *service.TargetService
	events          *service.EventService
	db              *sql.DB
	logger          *slog.Logger
	payloadMaxBytes int64
	authEnabled     bool
	apiKey          string
	metrics         *observability.Metrics
}

func NewServer(targets *service.TargetService, events *service.EventService, db *sql.DB, logger *slog.Logger, payloadMaxBytes int64, authEnabled bool, apiKey string, metrics *observability.Metrics) *Server {
	return &Server{
		targets:         targets,
		events:          events,
		db:              db,
		logger:          logger,
		payloadMaxBytes: payloadMaxBytes,
		authEnabled:     authEnabled,
		apiKey:          apiKey,
		metrics:         metrics,
	}
}

// Handler builds the complete HTTP handler with routing and middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("GET /metrics", s.metrics.Handler)

	mux.HandleFunc("POST /api/v1/targets", s.handleCreateTarget)
	mux.HandleFunc("GET /api/v1/targets/{target_id}", s.handleGetTarget)
	mux.HandleFunc("DELETE /api/v1/targets/{target_id}", s.handleDeleteTarget)

	mux.HandleFunc("POST /api/v1/targets/{target_id}/events", s.handleCreateEvent)
	mux.HandleFunc("GET /api/v1/events", s.handleListEvents)
	mux.HandleFunc("GET /api/v1/events/{event_id}", s.handleGetEvent)
	mux.HandleFunc("GET /api/v1/events/{event_id}/attempts", s.handleListEventAttempts)
	mux.HandleFunc("POST /api/v1/events/{event_id}/retry", s.handleReplayEvent)

	var handler http.Handler = mux
	handler = apiKeyMiddleware(s.authEnabled, s.apiKey)(handler)
	handler = loggingMiddleware(s.logger, s.metrics)(handler)
	handler = requestIDMiddleware(handler)
	return handler
}
