package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/metrics"
)

func TestServerHandlers(t *testing.T) {
	m := metrics.NewMetrics("test-region", "v1.8.23")
	m.SetSubscriptionState(5, time.Now(), 0)
	srv := NewServer(":8080", m)

	// Test healthz (default live = true)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for /healthz, got %d", rec.Code)
	}

	// Test readyz (default false)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for /readyz when not ready, got %d", rec.Code)
	}

	// Set ready = true
	srv.SetReady(true)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for /readyz when ready, got %d", rec.Code)
	}

	// Test aliveCheck failing
	isAlive := true
	srv.SetAliveCheck(func() bool { return isAlive })

	isAlive = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for /healthz when aliveCheck is false, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for /readyz when aliveCheck is false, got %d", rec.Code)
	}

	// Restore alive
	isAlive = true
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for /readyz when alive restored, got %d", rec.Code)
	}

	// Test metrics
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for /metrics, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "xray_prober_subscription_endpoints_count") {
		t.Errorf("metrics response missing expected metric, got: %s", body)
	}
}
