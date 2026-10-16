package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/metrics"
)

func TestServer_StartAndShutdown(t *testing.T) {
	prom := metrics.NewMetrics("test-reg", "v26.0.0")
	// Use port 0 to choose free port
	s := NewServer("127.0.0.1:0", prom)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start()
	}()

	time.Sleep(50 * time.Millisecond)

	s.SetLive(false)
	s.SetReady(false)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	s.handleHealthz(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when not live, got %d", w.Code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}

	err := <-errCh
	if err != nil && err != http.ErrServerClosed {
		t.Errorf("unexpected start error: %v", err)
	}
}
