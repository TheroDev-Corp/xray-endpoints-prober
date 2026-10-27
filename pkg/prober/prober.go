package prober

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/config"
	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/metrics"
	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/subscription"
)

type Prober struct {
	cfg     *config.Config
	metrics *metrics.Metrics

	mu        sync.RWMutex
	endpoints []subscription.Endpoint

	speedtestActiveMu sync.Mutex

	clientMu sync.Mutex
	clients  map[int]*http.Client
}

func NewProber(cfg *config.Config, m *metrics.Metrics) *Prober {
	return &Prober{
		cfg:     cfg,
		metrics: m,
		clients: make(map[int]*http.Client),
	}
}

// getClient returns a cached HTTP client for localPort, or creates and caches a new one
func (p *Prober) getClient(localPort int, timeout time.Duration) *http.Client {
	p.clientMu.Lock()
	defer p.clientMu.Unlock()

	if p.clients == nil {
		p.clients = make(map[int]*http.Client)
	}

	if client, ok := p.clients[localPort]; ok {
		return client
	}

	client := createSocksClient(localPort, timeout)
	p.clients[localPort] = client
	return client
}

// cleanupClients closes idle connections and removes clients for ports that are no longer active
func (p *Prober) cleanupClients(activePorts map[int]bool) {
	p.clientMu.Lock()
	defer p.clientMu.Unlock()

	for port, client := range p.clients {
		if !activePorts[port] {
			if transport, ok := client.Transport.(*http.Transport); ok {
				transport.CloseIdleConnections()
			}
			delete(p.clients, port)
		}
	}
}

// Close closes all active clients and their idle connections
func (p *Prober) Close() {
	p.clientMu.Lock()
	defer p.clientMu.Unlock()

	for port, client := range p.clients {
		if transport, ok := client.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
		delete(p.clients, port)
	}
}

// UpdateEndpoints updates the active endpoints under probe
func (p *Prober) UpdateEndpoints(endpoints []subscription.Endpoint) {
	p.mu.Lock()
	p.endpoints = make([]subscription.Endpoint, len(endpoints))
	copy(p.endpoints, endpoints)
	activeIDs := make(map[string]bool)
	activePorts := make(map[int]bool)
	for _, ep := range endpoints {
		activeIDs[ep.ID] = true
		if ep.LocalPort > 0 {
			activePorts[ep.LocalPort] = true
		}
		p.metrics.RegisterEndpoint(ep.ID, ep.Name, ep.Protocol, ep.Server, ep.Port)
	}
	p.mu.Unlock()

	p.cleanupClients(activePorts)
	p.metrics.PruneMissing(activeIDs)
}

func (p *Prober) GetEndpoints() []subscription.Endpoint {
	p.mu.RLock()
	defer p.mu.RUnlock()
	res := make([]subscription.Endpoint, len(p.endpoints))
	copy(res, p.endpoints)
	return res
}

// StartLatencyLoop starts the latency probe ticker across all configured targets
func (p *Prober) StartLatencyLoop(ctx context.Context) {
	ticker := time.NewTicker(p.cfg.ProbePingInterval)
	defer ticker.Stop()

	// Initial run after short delay to let Xray socket bind
	select {
	case <-ctx.Done():
		return
	case <-time.After(1 * time.Second):
		p.runLatencyProbes(ctx)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.runLatencyProbes(ctx)
		}
	}
}

// StartSpeedtestLoop starts the sequential speedtest probe ticker
func (p *Prober) StartSpeedtestLoop(ctx context.Context) {
	if !p.cfg.ProbeSpeedtestEnabled {
		return
	}

	ticker := time.NewTicker(p.cfg.ProbeSpeedtestInterval)
	defer ticker.Stop()

	// Wait 1 second on boot to ensure Xray sockets are listening, then run initial speedtest
	select {
	case <-ctx.Done():
		return
	case <-time.After(1 * time.Second):
		p.runSpeedtestProbes(ctx)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.runSpeedtestProbes(ctx)
		}
	}
}

func (p *Prober) runLatencyProbes(ctx context.Context) {
	endpoints := p.GetEndpoints()
	if len(endpoints) == 0 {
		return
	}

	targets := p.cfg.ProbeTargets
	if len(targets) == 0 {
		return
	}

	concurrency := p.cfg.ProbeConcurrency
	if concurrency <= 0 {
		concurrency = 5
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var successCount int64
	var failCount int64
	cycleStart := time.Now()

	for _, ep := range endpoints {
		if ep.LocalPort <= 0 {
			continue
		}

		client := p.getClient(ep.LocalPort, p.cfg.ProbePingTimeout)

		for _, tgt := range targets {
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}

			wg.Add(1)
			go func(endpoint subscription.Endpoint, target config.TargetConfig, c *http.Client) {
				defer func() {
					<-sem
					wg.Done()
				}()

				probeCtx, cancel := context.WithTimeout(ctx, p.cfg.ProbePingTimeout)
				defer cancel()

				totalDur, phases, statusCode, err := probeEndpointDetailedWithClient(probeCtx, c, endpoint.LocalPort, target.URL, p.cfg.ProbeDNSServers)
				if err != nil {
					p.metrics.RecordTargetLatency(endpoint.ID, target.Tag, false, statusCode, 0, metrics.PhaseMetrics{}, err)
					atomic.AddInt64(&failCount, 1)
				} else {
					p.metrics.RecordTargetLatency(endpoint.ID, target.Tag, true, statusCode, totalDur, phases, nil)
					atomic.AddInt64(&successCount, 1)
				}
			}(ep, tgt, client)
		}
	}

	wg.Wait()
	log.Printf("[prober:ping] cycle complete for %d endpoints x %d targets in %s (UP: %d, DOWN: %d)",
		len(endpoints), len(targets), time.Since(cycleStart).Round(time.Millisecond), successCount, failCount)
}

func (p *Prober) runSpeedtestProbes(ctx context.Context) {
	if !p.cfg.ProbeSpeedtestEnabled {
		return
	}

	// Sequential execution
	if !p.speedtestActiveMu.TryLock() {
		log.Printf("Speedtest is already in progress, skipping tick")
		return
	}
	defer p.speedtestActiveMu.Unlock()

	endpoints := p.GetEndpoints()
	if len(endpoints) == 0 {
		return
	}

	log.Printf("[prober:speedtest] starting sequential download test for %d endpoints...", len(endpoints))
	speedStart := time.Now()
	for idx, ep := range endpoints {
		if ep.LocalPort <= 0 {
			continue
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		probeCtx, cancel := context.WithTimeout(ctx, p.cfg.ProbeSpeedtestTimeout)
		client := p.getClient(ep.LocalPort, p.cfg.ProbeSpeedtestTimeout)
		bytesRead, duration, err := probeEndpointSpeedWithClient(probeCtx, client, ep.LocalPort, p.cfg.ProbeSpeedtestURL)
		cancel()

		p.metrics.RecordSpeed(ep.ID, bytesRead, duration, err)
		if err != nil {
			log.Printf("[prober:speedtest] [%d/%d] %s (%s) error: %v", idx+1, len(endpoints), ep.Name, ep.Protocol, err)
		} else {
			speedMbps := (float64(bytesRead) * 8) / (duration.Seconds() * 1000 * 1000)
			log.Printf("[prober:speedtest] [%d/%d] %s (%s): %.2f Mbps (%d bytes in %s)",
				idx+1, len(endpoints), ep.Name, ep.Protocol, speedMbps, bytesRead, duration.Round(time.Millisecond))
		}
	}
	log.Printf("[prober:speedtest] cycle complete for %d endpoints in %s",
		len(endpoints), time.Since(speedStart).Round(time.Millisecond))
}

// createSocksClient creates an HTTP client routing through local SOCKS5 proxy on 127.0.0.1:localPort
func createSocksClient(localPort int, timeout time.Duration) *http.Client {
	proxyURL, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", localPort))
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		DialContext: (&net.Dialer{
			Timeout:   timeout,
			KeepAlive: 15 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   5,
		IdleConnTimeout:       15 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}

// probeEndpointDetailed measures TTFB, total latency and connection phases with httptrace
func probeEndpointDetailed(ctx context.Context, localPort int, targetURL string, dnsServers []string) (time.Duration, metrics.PhaseMetrics, int, error) {
	client := createSocksClient(localPort, 10*time.Second)
	defer func() {
		if transport, ok := client.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}()
	return probeEndpointDetailedWithClient(ctx, client, localPort, targetURL, dnsServers)
}

func probeEndpointDetailedWithClient(ctx context.Context, client *http.Client, localPort int, targetURL string, dnsServers []string) (time.Duration, metrics.PhaseMetrics, int, error) {
	var (
		dnsStart, dnsDone   time.Time
		connStart, connDone time.Time
		tlsStart, tlsDone   time.Time
		gotFirstByte        time.Time
		phases              metrics.PhaseMetrics
	)

	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			dnsStart = time.Now()
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			dnsDone = time.Now()
			if !dnsStart.IsZero() {
				phases.DNSDurationSec = dnsDone.Sub(dnsStart).Seconds()
			}
		},
		ConnectStart: func(network, addr string) {
			connStart = time.Now()
		},
		ConnectDone: func(network, addr string, err error) {
			connDone = time.Now()
			if !connStart.IsZero() {
				phases.TCPDurationSec = connDone.Sub(connStart).Seconds()
			}
		},
		TLSHandshakeStart: func() {
			tlsStart = time.Now()
		},
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			tlsDone = time.Now()
			if !tlsStart.IsZero() {
				phases.TLSDurationSec = tlsDone.Sub(tlsStart).Seconds()
			}
		},
		GotFirstResponseByte: func() {
			gotFirstByte = time.Now()
		},
	}

	reqCtx := httptrace.WithClientTrace(ctx, trace)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, phases, 0, err
	}
	req.Header.Set("User-Agent", "xray-endpoints-prober/1.0")

	start := time.Now()
	resp, err := client.Do(req)
	totalDur := time.Since(start)
	if err != nil {
		return totalDur, phases, 0, err
	}
	defer resp.Body.Close()

	if !gotFirstByte.IsZero() {
		phases.TTFBDurationSec = gotFirstByte.Sub(start).Seconds()
	} else {
		phases.TTFBDurationSec = totalDur.Seconds()
	}
	phases.TotalDurationSec = totalDur.Seconds()

	// Drain response body up to 16KB so HTTP connection can be reused cleanly
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16*1024))

	statusCode := resp.StatusCode
	if statusCode < 200 || statusCode >= 400 {
		return totalDur, phases, statusCode, fmt.Errorf("unexpected HTTP status: %d", statusCode)
	}

	if phases.DNSDurationSec == 0 {
		if parsedU, err := url.Parse(targetURL); err == nil && parsedU.Hostname() != "" {
			if dnsDur, err := measureDNSResolution(ctx, localPort, parsedU.Hostname(), dnsServers, 2*time.Second); err == nil && dnsDur > 0 {
				phases.DNSDurationSec = dnsDur.Seconds()
			}
		}
	}
	return totalDur, phases, statusCode, nil
}

func probeEndpointSpeed(ctx context.Context, localPort int, targetURL string) (int64, time.Duration, error) {
	client := createSocksClient(localPort, 30*time.Second)
	defer func() {
		if transport, ok := client.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}()
	return probeEndpointSpeedWithClient(ctx, client, localPort, targetURL)
}

func probeEndpointSpeedWithClient(ctx context.Context, client *http.Client, localPort int, targetURL string) (int64, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("User-Agent", "xray-endpoints-prober/1.0")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return 0, 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Read body until EOF or context cancel
	bytesRead, err := io.Copy(io.Discard, resp.Body)
	duration := time.Since(start)
	if err != nil && ctx.Err() == nil {
		return bytesRead, duration, err
	}

	return bytesRead, duration, nil
}

// measureDNSResolution measures real DNS lookup latency via SOCKS5 proxy using DNS-over-TCP to configured DNS servers
func measureDNSResolution(ctx context.Context, proxyPort int, host string, dnsServers []string, timeout time.Duration) (time.Duration, error) {
	if ip := net.ParseIP(host); ip != nil {
		return 0, nil
	}
	if len(dnsServers) == 0 {
		dnsServers = []string{"1.1.1.1:53", "8.8.8.8:53"}
	}
	var lastErr error
	for _, dnsServer := range dnsServers {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}

		dnsCtx, cancel := context.WithTimeout(ctx, timeout)
		dur, err := queryDNSOverSocks(dnsCtx, proxyPort, dnsServer, host, timeout)
		cancel()
		if err == nil {
			return dur, nil
		}
		lastErr = err
	}
	return 0, lastErr
}

func queryDNSOverSocks(ctx context.Context, proxyPort int, dnsServer, host string, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	conn, err := socks5Dial(ctx, fmt.Sprintf("127.0.0.1:%d", proxyPort), dnsServer, timeout)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	query := makeDNSQuery(host)
	prefix := make([]byte, 2)
	binary.BigEndian.PutUint16(prefix, uint16(len(query)))

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)

	if _, err := conn.Write(prefix); err != nil {
		return 0, err
	}
	if _, err := conn.Write(query); err != nil {
		return 0, err
	}

	if _, err := io.ReadFull(conn, prefix); err != nil {
		return 0, err
	}
	respLen := binary.BigEndian.Uint16(prefix)
	if respLen > 4096 {
		return 0, fmt.Errorf("dns response too large: %d", respLen)
	}
	respBuf := make([]byte, respLen)
	if _, err := io.ReadFull(conn, respBuf); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

func socks5Dial(ctx context.Context, proxyAddr, dstAddr string, timeout time.Duration) (net.Conn, error) {
	d := &net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}

	// CRITICAL: Immediately set deadline so handshake and connect never hang indefinitely!
	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)

	// 1. Handshake
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		conn.Close()
		return nil, err
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil || buf[0] != 0x05 || buf[1] != 0x00 {
		conn.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("socks5 handshake failed: unsupported auth")
	}

	// 2. Connect
	host, portStr, err := net.SplitHostPort(dstAddr)
	if err != nil {
		conn.Close()
		return nil, err
	}
	port := 53
	fmt.Sscanf(portStr, "%d", &port)

	ip := net.ParseIP(host)
	req := []byte{0x05, 0x01, 0x00}
	if ip4 := ip.To4(); ip4 != nil {
		req = append(req, 0x01)
		req = append(req, ip4...)
	} else if ip6 := ip.To16(); ip6 != nil && !strings.Contains(host, ".") {
		req = append(req, 0x04)
		req = append(req, ip6...)
	} else {
		req = append(req, 0x03, byte(len(host)))
		req = append(req, []byte(host)...)
	}
	req = append(req, byte(port>>8), byte(port&0xFF))

	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, err
	}

	resp := make([]byte, 4)
	if _, err := io.ReadFull(conn, resp); err != nil {
		conn.Close()
		return nil, err
	}
	if resp[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect failed with status 0x%02x", resp[1])
	}
	switch resp[3] {
	case 0x01:
		if _, err := io.CopyN(io.Discard, conn, 4+2); err != nil {
			conn.Close()
			return nil, err
		}
	case 0x03:
		var l [1]byte
		if _, err := io.ReadFull(conn, l[:]); err != nil {
			conn.Close()
			return nil, err
		}
		if _, err := io.CopyN(io.Discard, conn, int64(l[0])+2); err != nil {
			conn.Close()
			return nil, err
		}
	case 0x04:
		if _, err := io.CopyN(io.Discard, conn, 16+2); err != nil {
			conn.Close()
			return nil, err
		}
	default:
		conn.Close()
		return nil, fmt.Errorf("socks5 connect returned unsupported address type: 0x%02x", resp[3])
	}
	return conn, nil
}

func makeDNSQuery(domain string) []byte {
	var q []byte
	q = append(q, 0x12, 0x34)
	q = append(q, 0x01, 0x00)
	q = append(q, 0x00, 0x01)
	q = append(q, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00)
	for _, p := range strings.Split(domain, ".") {
		q = append(q, byte(len(p)))
		q = append(q, []byte(p)...)
	}
	q = append(q, 0x00, 0x00, 0x01, 0x00, 0x01)
	return q
}
