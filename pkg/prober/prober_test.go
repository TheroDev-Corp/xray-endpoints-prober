package prober

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/config"
	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/metrics"
	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/subscription"
)

func TestProberUpdateEndpoints(t *testing.T) {
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

	eps := []subscription.Endpoint{
		{
			ID:        "ep-1",
			Name:      "Server 1",
			Protocol:  "vless",
			Server:    "1.1.1.1",
			Port:      443,
			LocalPort: 10800,
		},
	}

	p.UpdateEndpoints(eps)
	current := p.GetEndpoints()
	if len(current) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(current))
	}
	if current[0].ID != "ep-1" {
		t.Errorf("expected ID ep-1, got %s", current[0].ID)
	}
}

func TestProberSpeedtestDisabled(t *testing.T) {
	cfg := &config.Config{
		Region:                 "test-region",
		ProbeConcurrency:       2,
		ProbePingInterval:      1 * time.Second,
		ProbePingTimeout:       500 * time.Millisecond,
		ProbeSpeedtestEnabled:  false,
		ProbeSpeedtestInterval: 10 * time.Millisecond,
		ProbeSpeedtestTimeout:  100 * time.Millisecond,
	}
	m := metrics.NewMetrics("test-region", "test-version")
	p := NewProber(cfg, m)

	eps := []subscription.Endpoint{
		{
			ID:        "ep-1",
			Name:      "Server 1",
			Protocol:  "vless",
			Server:    "1.1.1.1",
			Port:      443,
			LocalPort: 10800,
		},
	}
	p.UpdateEndpoints(eps)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Should return immediately because ProbeSpeedtestEnabled is false
	done := make(chan struct{})
	go func() {
		p.StartSpeedtestLoop(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Succeeded immediately
	case <-time.After(500 * time.Millisecond):
		t.Fatal("StartSpeedtestLoop did not return immediately when speedtest is disabled")
	}

	// Also runSpeedtestProbes directly should be a no-op
	p.runSpeedtestProbes(ctx)
}

// Simple dummy SOCKS5 proxy server for testing
func runMockSocks5(t *testing.T, targetAddr string) (int, func(), error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, nil, err
	}
	port := l.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					continue
				}
			}
			go handleSocks5(conn, targetAddr)
		}
	}()

	return port, func() {
		cancel()
		_ = l.Close()
	}, nil
}

func handleSocks5(client net.Conn, targetAddr string) {
	defer client.Close()
	buf := make([]byte, 256)
	// Read auth negotiation
	n, err := client.Read(buf)
	if err != nil || n < 2 || buf[0] != 0x05 {
		return
	}
	// Reply no auth
	_, _ = client.Write([]byte{0x05, 0x00})

	// Read connect request
	n, err = client.Read(buf)
	if err != nil || n < 4 || buf[1] != 0x01 {
		return
	}

	// Dial target
	target, err := net.Dial("tcp4", targetAddr)
	if err != nil {
		_, _ = client.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer target.Close()

	// Reply success
	_, _ = client.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 80})

	// Proxy bytes
	go func() {
		b := make([]byte, 4096)
		for {
			nr, er := client.Read(b)
			if nr > 0 {
				_, ew := target.Write(b[:nr])
				if ew != nil {
					break
				}
			}
			if er != nil {
				break
			}
		}
	}()

	b := make([]byte, 4096)
	for {
		nr, er := target.Read(b)
		if nr > 0 {
			_, ew := client.Write(b[:nr])
			if ew != nil {
				break
			}
		}
		if er != nil {
			break
		}
	}
}

func TestProbeLatencyAndSpeed(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen on tcp4 in sandbox: %v", err)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("pong"))
			return
		}
		if r.URL.Path == "/speed" {
			w.WriteHeader(http.StatusOK)
			payload := make([]byte, 1024*128)
			_, _ = w.Write(payload)
			return
		}
		http.NotFound(w, r)
	}))
	ts.Listener = l
	ts.Start()
	defer ts.Close()

	socksPort, closeSocks, err := runMockSocks5(t, ts.Listener.Addr().String())
	if err != nil {
		t.Skipf("cannot listen socks5: %v", err)
	}
	defer closeSocks()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pingURL := fmt.Sprintf("http://127.0.0.1:%d/ping", ts.Listener.Addr().(*net.TCPAddr).Port)
	totalDur, phases, status, err := probeEndpointDetailed(ctx, socksPort, pingURL, nil)
	if err != nil {
		t.Fatalf("probeEndpointDetailed failed: %v", err)
	}
	if totalDur <= 0 {
		t.Errorf("expected positive totalDur, got %v", totalDur)
	}
	if status != http.StatusOK {
		t.Errorf("expected status 200, got %d", status)
	}
	if phases.TTFBDurationSec <= 0 {
		t.Errorf("expected positive TTFBDurationSec, got %v", phases.TTFBDurationSec)
	}

	speedURL := fmt.Sprintf("http://127.0.0.1:%d/speed", ts.Listener.Addr().(*net.TCPAddr).Port)
	bytesRead, dur, err := probeEndpointSpeed(ctx, socksPort, speedURL)
	if err != nil {
		t.Fatalf("probeEndpointSpeed failed: %v", err)
	}
	if bytesRead < 1024*128 {
		t.Errorf("expected >= 128KB read, got %d", bytesRead)
	}
	if dur <= 0 {
		t.Errorf("expected positive duration, got %v", dur)
	}
}
