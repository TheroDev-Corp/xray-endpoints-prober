package subscription

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/config"
)

func TestSubscriptionManager_PollAndCallback(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sub-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cacheFile := filepath.Join(tempDir, "cache.txt")

	rawContent := "vless://b3c6248c-48d7-4fe9-b77f-7fc9d26fa901@1.1.1.1:443?encryption=none&type=tcp#Node1\n" +
		"trojan://password123@2.2.2.2:443#Node2\n"
	b64Content := base64.StdEncoding.EncodeToString([]byte(rawContent))

	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(b64Content))
	}))
	defer server.Close()

	cfg := &config.Config{
		SubscriptionURL:          server.URL,
		SubscriptionCacheFile:    cacheFile,
		SubscriptionPollInterval: 100 * time.Millisecond,
		ProtocolsExclude:         []string{"trojan"},
	}

	var updateCount int32
	var receivedEndpoints []Endpoint
	onUpdate := func(eps []Endpoint) error {
		atomic.AddInt32(&updateCount, 1)
		receivedEndpoints = eps
		return nil
	}

	mgr := NewManager(cfg, onUpdate)

	// Poll
	ctx := context.Background()
	if err := mgr.Poll(ctx); err != nil {
		t.Fatalf("mgr.Poll() failed: %v", err)
	}

	// Verification
	if atomic.LoadInt32(&updateCount) != 1 {
		t.Errorf("expected 1 update callback call, got %d", atomic.LoadInt32(&updateCount))
	}
	if len(receivedEndpoints) != 1 {
		t.Fatalf("expected 1 active endpoint (trojan excluded), got %d", len(receivedEndpoints))
	}
	if receivedEndpoints[0].Protocol != "vless" {
		t.Errorf("expected vless protocol, got %s", receivedEndpoints[0].Protocol)
	}

	// Verify cache file was written
	cachedData, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatalf("failed to read cache file: %v", err)
	}
	if string(cachedData) != b64Content {
		t.Errorf("cache file content mismatch")
	}

	// Second poll with same hash should not trigger onUpdate
	if err := mgr.Poll(ctx); err != nil {
		t.Fatalf("second mgr.Poll() failed: %v", err)
	}
	if atomic.LoadInt32(&updateCount) != 1 {
		t.Errorf("expected update count to remain 1 for unchanged content, got %d", atomic.LoadInt32(&updateCount))
	}

	// Third poll: same hash, but aliveCheck returns false (process dead) -> should re-trigger onUpdate!
	isProcessAlive := false
	mgr.SetAliveCheck(func() bool { return isProcessAlive })
	if err := mgr.Poll(ctx); err != nil {
		t.Fatalf("third mgr.Poll() failed: %v", err)
	}
	if atomic.LoadInt32(&updateCount) != 2 {
		t.Errorf("expected update count to increase to 2 when process is dead, got %d", atomic.LoadInt32(&updateCount))
	}

	if mgr.GetPollErrors() != 0 {
		t.Errorf("expected 0 poll errors, got %d", mgr.GetPollErrors())
	}
	if mgr.GetLastPollTime().IsZero() {
		t.Errorf("expected last poll time to be set")
	}
	if len(mgr.GetEndpoints()) != 1 {
		t.Errorf("expected 1 endpoint in manager state")
	}
}

func TestSubscriptionManager_FallbackToCache(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sub-test-cache-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cacheFile := filepath.Join(tempDir, "cache.txt")
	rawContent := "vless://b3c6248c-48d7-4fe9-b77f-7fc9d26fa901@1.1.1.1:443?encryption=none&type=tcp#NodeCached\n"
	b64Content := base64.StdEncoding.EncodeToString([]byte(rawContent))
	if err := os.WriteFile(cacheFile, []byte(b64Content), 0644); err != nil {
		t.Fatalf("failed to write initial cache file: %v", err)
	}

	// Server returning 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.Config{
		SubscriptionURL:          server.URL,
		SubscriptionCacheFile:    cacheFile,
		SubscriptionPollInterval: 100 * time.Millisecond,
	}

	var updateCount int32
	mgr := NewManager(cfg, func(eps []Endpoint) error {
		atomic.AddInt32(&updateCount, 1)
		return nil
	})

	ctx := context.Background()
	err = mgr.Poll(ctx)
	if err == nil {
		t.Errorf("expected Poll error due to 500, got nil")
	}

	// Should have incremented error
	if mgr.GetPollErrors() != 1 {
		t.Errorf("expected 1 poll error, got %d", mgr.GetPollErrors())
	}

	// Should have successfully loaded from cache
	eps := mgr.GetEndpoints()
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint loaded from cache fallback, got %d", len(eps))
	}
	if eps[0].Name != "NodeCached" {
		t.Errorf("expected endpoint name NodeCached, got %s", eps[0].Name)
	}
}

func TestSubscriptionManager_StartLoop(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sub-test-loop-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cacheFile := filepath.Join(tempDir, "cache.txt")
	rawContent := "vless://uuid@1.1.1.1:443#LoopNode\n"
	b64Content := base64.StdEncoding.EncodeToString([]byte(rawContent))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(b64Content))
	}))
	defer server.Close()

	cfg := &config.Config{
		SubscriptionURL:          server.URL,
		SubscriptionCacheFile:    cacheFile,
		SubscriptionPollInterval: 50 * time.Millisecond,
	}

	mgr := NewManager(cfg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("mgr.Start() failed: %v", err)
	}

	time.Sleep(120 * time.Millisecond)
	if len(mgr.GetEndpoints()) != 1 {
		t.Errorf("expected 1 endpoint after loop run")
	}
}
