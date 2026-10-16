package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/metrics"
)

type Server struct {
	httpServer *http.Server
	metrics    *metrics.Metrics
	isReady    atomic.Bool
	isLive     atomic.Bool
}

func NewServer(listenAddr string, m *metrics.Metrics) *Server {
	s := &Server{
		metrics: m,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)

	s.httpServer = &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	s.isLive.Store(true)
	s.isReady.Store(false)

	return s
}

func (s *Server) SetReady(ready bool) {
	s.isReady.Store(ready)
}

func (s *Server) SetLive(live bool) {
	s.isLive.Store(live)
}

func (s *Server) Start() error {
	log.Printf("Starting HTTP server on %s", s.httpServer.Addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server failed: %w", err)
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	s.metrics.WritePrometheus(w)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if s.isLive.Load() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK\n"))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("Unhealthy\n"))
	}
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.isReady.Load() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Ready\n"))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("Not Ready\n"))
	}
}
