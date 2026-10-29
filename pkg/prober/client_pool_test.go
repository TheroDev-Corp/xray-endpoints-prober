package prober

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/config"
	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/metrics"
	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/subscription"
)

func TestProberClientPoolReuseAndCleanup(t *testing.T) {
	cfg := &config.Config{
		Region:                 "test-region",
		ProbeConcurrency:       2,
		ProbePingInterval:      1 * time.Second,
		ProbePingTimeout:       500 * time.Millisecond,
		ProbeSpeedtestInterval: 2 * time.Second,
		ProbeSpeedtestTimeout:  1 * time.Second,
	}
	m := metrics.NewMetrics("test-region", "test-version")
	p := NewProber(cfg, m)

	// 1. Getting client for port 10800 twice should return the exact same *http.Client instance
	c1 := p.getClient(10800, 5*time.Second)
	if c1 == nil {
		t.Fatal("expected non-nil client")
	}

	c2 := p.getClient(10800, 5*time.Second)
	if c1 != c2 {
		t.Errorf("expected client reuse: got different client instances (%p != %p)", c1, c2)
	}

	// 2. Getting client for a different port should return a distinct client
	c3 := p.getClient(10801, 5*time.Second)
	if c1 == c3 {
		t.Errorf("expected different clients for different ports (%p == %p)", c1, c3)
	}

	// 3. UpdateEndpoints with only port 10800 should purge port 10801 from pool
	p.UpdateEndpoints([]subscription.Endpoint{
		{
			ID:        "ep-1",
			Name:      "Node 1",
			Protocol:  "vless",
			Server:    "1.1.1.1",
			Port:      443,
			LocalPort: 10800,
		},
	})

	p.clientMu.Lock()
	_, has10800 := p.clients[10800]
	_, has10801 := p.clients[10801]
	p.clientMu.Unlock()

	if !has10800 {
		t.Errorf("expected port 10800 to remain in client pool")
	}
	if has10801 {
		t.Errorf("expected port 10801 to be purged from client pool")
	}

	// 4. Close() should empty the pool
	p.Close()
	p.clientMu.Lock()
	count := len(p.clients)
	p.clientMu.Unlock()

	if count != 0 {
		t.Errorf("expected empty client pool after Close(), got %d", count)
	}
}

func TestSocks5DialTimeoutOnHangingServer(t *testing.T) {
	// Start a listener that accepts connection but never writes anything (simulates hanging proxy)
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen on tcp4 in sandbox: %v", err)
	}
	defer l.Close()

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			// Just hold connection open without sending SOCKS5 greeting response
			go func(c net.Conn) {
				time.Sleep(2 * time.Second)
				_ = c.Close()
			}(conn)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	conn, err := socks5Dial(ctx, l.Addr().String(), "1.1.1.1:53", 150*time.Millisecond)
	elapsed := time.Since(start)

	if conn != nil {
		conn.Close()
		t.Fatal("expected socks5Dial to fail on hanging server")
	}
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	// Must fail fast within timeout and not hang indefinitely!
	if elapsed > 1*time.Second {
		t.Errorf("socks5Dial took too long to time out: %v", elapsed)
	}
}

func TestProberResetClients(t *testing.T) {
	cfg := &config.Config{
		Region: "test-region",
	}
	m := metrics.NewMetrics("test-region", "test-version")
	p := NewProber(cfg, m)

	c1 := p.getClient(10800, 5*time.Second)
	if c1 == nil {
		t.Fatal("expected non-nil client")
	}

	// ResetClients should close idle connections without error and not panic
	p.ResetClients()

	// Client reference should still remain valid and usable
	c2 := p.getClient(10800, 5*time.Second)
	if c1 != c2 {
		t.Errorf("expected client to remain in pool after ResetClients")
	}
}
