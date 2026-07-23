package health

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

type Server struct {
	address string
	logger  *slog.Logger
	ready   atomic.Bool
	server  *http.Server
}

func New(address string, logger *slog.Logger) *Server {
	server := &Server{
		address: address,
		logger:  logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.handleHealth)
	mux.HandleFunc("/readyz", server.handleReady)
	server.server = &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	return server
}

func (s *Server) Start() {
	go func() {
		s.logger.Info("starting health server", slog.String("address", s.address))
		if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("health server failed", slog.Any("error", err))
		}
	}()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *Server) MarkReady() {
	s.ready.Store(true)
}

func (s *Server) handleHealth(response http.ResponseWriter, _ *http.Request) {
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("ok\n"))
}

func (s *Server) handleReady(response http.ResponseWriter, _ *http.Request) {
	if !s.ready.Load() {
		http.Error(response, "not ready", http.StatusServiceUnavailable)
		return
	}

	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("ready\n"))
}
