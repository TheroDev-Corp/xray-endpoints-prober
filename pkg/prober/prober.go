package prober

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
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
}

func NewProber(cfg *config.Config, m *metrics.Metrics) *Prober {
	return &Prober{
		cfg:     cfg,
		metrics: m,
	}
}

// UpdateEndpoints updates the active endpoints under probe
func (p *Prober) UpdateEndpoints(endpoints []subscription.Endpoint) {
	p.mu.Lock()
	p.endpoints = make([]subscription.Endpoint, len(endpoints))
	copy(p.endpoints, endpoints)
	activeIDs := make(map[string]bool)
	for _, ep := range endpoints {
		activeIDs[ep.ID] = true
		p.metrics.RegisterEndpoint(ep.ID, ep.Name, ep.Protocol, ep.Server, ep.Port)
	}
	p.mu.Unlock()

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

		for _, tgt := range targets {
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}

			wg.Add(1)
			go func(endpoint subscription.Endpoint, target config.TargetConfig) {
				defer func() {
					<-sem
					wg.Done()
				}()

				probeCtx, cancel := context.WithTimeout(ctx, p.cfg.ProbePingTimeout)
				defer cancel()

				totalDur, phases, statusCode, err := probeEndpointDetailed(probeCtx, endpoint.LocalPort, target.URL)
				if err != nil {
					p.metrics.RecordTargetLatency(endpoint.ID, target.Tag, false, statusCode, 0, metrics.PhaseMetrics{}, err)
					atomic.AddInt64(&failCount, 1)
				} else {
					p.metrics.RecordTargetLatency(endpoint.ID, target.Tag, true, statusCode, totalDur, phases, nil)
					atomic.AddInt64(&successCount, 1)
				}
			}(ep, tgt)
		}
	}

	wg.Wait()
	log.Printf("[prober:ping] cycle complete for %d endpoints x %d targets in %s (UP: %d, DOWN: %d)",
		len(endpoints), len(targets), time.Since(cycleStart).Round(time.Millisecond), successCount, failCount)
}

func (p *Prober) runSpeedtestProbes(ctx context.Context) {
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
		bytesRead, duration, err := probeEndpointSpeed(probeCtx, ep.LocalPort, p.cfg.ProbeSpeedtestURL)
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
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}

// probeEndpointDetailed measures TTFB, total latency and connection phases with httptrace
func probeEndpointDetailed(ctx context.Context, localPort int, targetURL string) (time.Duration, metrics.PhaseMetrics, int, error) {
	client := createSocksClient(localPort, 10*time.Second)
	defer func() {
		if transport, ok := client.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}()

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

	// Drain a small portion of body
	_, _ = io.CopyN(io.Discard, resp.Body, 1024)

	statusCode := resp.StatusCode
	if statusCode < 200 || statusCode >= 400 {
		return totalDur, phases, statusCode, fmt.Errorf("unexpected HTTP status: %d", statusCode)
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
