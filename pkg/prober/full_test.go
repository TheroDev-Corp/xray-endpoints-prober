package prober

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/config"
	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/metrics"
	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/subscription"
)

// mockDirectSocksProxy runs a simple TCP server acting as direct forwarder
func startDirectSocksProxy(t *testing.T, targetAddr string) (int, func()) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	stop := make(chan struct{})

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				select {
				case <-stop:
					return
				default:
					continue
				}
			}

			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 256)
				n, err := c.Read(buf)
				if err != nil || n < 2 || buf[0] != 0x05 {
					return
				}
				// Reply: SOCKS5, no auth
				_, _ = c.Write([]byte{0x05, 0x00})

				// Read connect request
				n, err = c.Read(buf)
				if err != nil || n < 4 {
					return
				}

				// Forward to real target
				targetConn, err := net.Dial("tcp", targetAddr)
				if err != nil {
					// SOCKS connection refused
					_, _ = c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
					return
				}
				defer targetConn.Close()

				// SOCKS success
				_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 80})

				// Bidirectional copy
				done := make(chan struct{}, 2)
				go func() {
					buf := make([]byte, 4096)
					for {
						nr, er := c.Read(buf)
						if nr > 0 {
							_, _ = targetConn.Write(buf[:nr])
						}
						if er != nil {
							break
						}
					}
					done <- struct{}{}
				}()
				go func() {
					buf := make([]byte, 4096)
					for {
						nr, er := targetConn.Read(buf)
						if nr > 0 {
							_, _ = c.Write(buf[:nr])
						}
						if er != nil {
							break
						}
					}
					done <- struct{}{}
				}()

				<-done
			}(conn)
		}
	}()

	port := l.Addr().(*net.TCPAddr).Port
	cleanup := func() {
		close(stop)
		_ = l.Close()
	}
	return port, cleanup
}

func TestProber_LatencyAndSpeedtestLoops(t *testing.T) {
	var targetCalls int32
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetCalls, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("0123456789abcdef0123456789abcdef"))
	}))
	defer targetServer.Close()

	socksPort, cleanup := startDirectSocksProxy(t, targetServer.Listener.Addr().String())
	defer cleanup()

	targetURL := fmt.Sprintf("http://%s/test", targetServer.Listener.Addr().String())

	cfg := &config.Config{
		Region: "test-reg",
		ProbeTargets: []config.TargetConfig{
			{URL: targetURL, Tag: "mock-target"},
		},
		ProbePingInterval:      50 * time.Millisecond,
		ProbePingTimeout:       1 * time.Second,
		ProbeSpeedtestURL:      targetURL,
		ProbeSpeedtestInterval: 50 * time.Millisecond,
		ProbeSpeedtestTimeout:  1 * time.Second,
		ProbeConcurrency:       2,
	}

	prom := metrics.NewMetrics("test-reg", "v26.0.0")
	p := NewProber(cfg, prom)

	endpoints := []subscription.Endpoint{
		{
			ID:        "ep-1",
			Name:      "Node 1",
			Protocol:  "vless",
			Server:    "1.1.1.1",
			Port:      443,
			LocalPort: socksPort,
		},
		{
			ID:        "ep-down",
			Name:      "Node Down",
			Protocol:  "trojan",
			Server:    "2.2.2.2",
			Port:      443,
			LocalPort: 64999, // Dead port
		},
	}
	p.UpdateEndpoints(endpoints)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Execute single latency run
	p.runLatencyProbes(ctx)

	// Execute single speedtest run
	p.runSpeedtestProbes(ctx)

	// Verify metrics recorded
	buf := new(bytes.Buffer)
	prom.WritePrometheus(buf)
	if buf.Len() == 0 {
		t.Fatalf("expected non-empty prometheus metrics export")
	}

	if atomic.LoadInt32(&targetCalls) == 0 {
		t.Errorf("expected mock target server to receive calls through socks")
	}
}
