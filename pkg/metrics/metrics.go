package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// Default histogram buckets in seconds for network latency
var LatencyBuckets = []float64{
	0.025, 0.05, 0.1, 0.2, 0.35, 0.5, 0.75, 1.0, 1.5, 2.5, 5.0, 10.0,
}

type Histogram struct {
	Count   uint64
	Sum     float64
	Buckets map[float64]uint64
}

func newHistogram() *Histogram {
	h := &Histogram{
		Buckets: make(map[float64]uint64, len(LatencyBuckets)),
	}
	for _, b := range LatencyBuckets {
		h.Buckets[b] = 0
	}
	return h
}

func (h *Histogram) Observe(v float64) {
	h.Count++
	h.Sum += v
	for _, b := range LatencyBuckets {
		if v <= b {
			h.Buckets[b]++
		}
	}
}

type PhaseMetrics struct {
	DNSDurationSec float64
	TCPDurationSec float64
	TLSDurationSec float64
	TTFBDurationSec float64
	TotalDurationSec float64
}

type TargetProbeMetrics struct {
	Up             int     // 1 or 0
	LatencySec     float64 // Total latency in seconds
	StatusCode     int     // HTTP status code (200, 204, etc.)
	ErrorsCount    int64
	ConsecutiveErr int64
	LastProbe      time.Time
	Phases         PhaseMetrics
	Histogram      *Histogram
}

type EndpointMetrics struct {
	// target tag -> TargetProbeMetrics
	Targets     map[string]*TargetProbeMetrics
	SpeedBps    float64 // Speed in bytes per second
	SpeedDurSec float64 // Speedtest duration in seconds
	SpeedErrors int64
}

type Metrics struct {
	mu sync.RWMutex

	region      string
	coreVersion string

	endpointsCount int
	lastPollTime   time.Time
	pollErrors     int64

	// key: ep.ID
	endpointMeta    map[string]map[string]string
	endpointMetrics map[string]*EndpointMetrics
}

func NewMetrics(region string, coreVersion string) *Metrics {
	return &Metrics{
		region:          region,
		coreVersion:     coreVersion,
		endpointMeta:    make(map[string]map[string]string),
		endpointMetrics: make(map[string]*EndpointMetrics),
	}
}

func (m *Metrics) SetCoreVersion(v string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.coreVersion = v
}

func (m *Metrics) SetSubscriptionState(count int, lastPoll time.Time, errors int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.endpointsCount = count
	m.lastPollTime = lastPoll
	m.pollErrors = errors
}

func (m *Metrics) RegisterEndpoint(id, name, protocol, server string, port int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.endpointMeta[id] = map[string]string{
		"id":       id,
		"name":     name,
		"protocol": protocol,
		"server":   server,
		"port":     fmt.Sprintf("%d", port),
		"region":   m.region,
	}
	if _, exists := m.endpointMetrics[id]; !exists {
		m.endpointMetrics[id] = &EndpointMetrics{
			Targets: make(map[string]*TargetProbeMetrics),
		}
	}
}

func (m *Metrics) RecordTargetLatency(id, target string, up bool, statusCode int, totalDur time.Duration, phases PhaseMetrics, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	met, exists := m.endpointMetrics[id]
	if !exists {
		met = &EndpointMetrics{
			Targets: make(map[string]*TargetProbeMetrics),
		}
		m.endpointMetrics[id] = met
	}

	tm, exists := met.Targets[target]
	if !exists {
		tm = &TargetProbeMetrics{
			Histogram: newHistogram(),
		}
		met.Targets[target] = tm
	}

	tm.LastProbe = time.Now()
	tm.StatusCode = statusCode

	if up && err == nil {
		tm.Up = 1
		tm.LatencySec = totalDur.Seconds()
		tm.Phases = phases
		tm.ConsecutiveErr = 0
		tm.Histogram.Observe(totalDur.Seconds())
	} else {
		tm.Up = 0
		tm.LatencySec = 0
		tm.ErrorsCount++
		tm.ConsecutiveErr++
		tm.Phases = PhaseMetrics{}
	}
}

// RecordLatency kept for backward compatibility with existing tests
func (m *Metrics) RecordLatency(id string, up bool, latency time.Duration, err error) {
	m.RecordTargetLatency(id, "default", up, 204, latency, PhaseMetrics{TotalDurationSec: latency.Seconds()}, err)
}

func (m *Metrics) RecordSpeed(id string, bytesRead int64, duration time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	met, exists := m.endpointMetrics[id]
	if !exists {
		met = &EndpointMetrics{
			Targets: make(map[string]*TargetProbeMetrics),
		}
		m.endpointMetrics[id] = met
	}

	if err != nil || duration <= 0 {
		met.SpeedErrors++
		return
	}

	met.SpeedDurSec = duration.Seconds()
	met.SpeedBps = float64(bytesRead) / duration.Seconds()
}

func (m *Metrics) PruneMissing(activeIDs map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.endpointMeta {
		if !activeIDs[id] {
			delete(m.endpointMeta, id)
			delete(m.endpointMetrics, id)
		}
	}
}

// WritePrometheus writes Prometheus exposition format
func (m *Metrics) WritePrometheus(w io.Writer) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	fmt.Fprintln(w, "# HELP xray_prober_core_version_info Version information about running Xray core")
	fmt.Fprintln(w, "# TYPE xray_prober_core_version_info gauge")
	fmt.Fprintf(w, "xray_prober_core_version_info{region=%q,version=%q} 1\n\n", m.region, m.coreVersion)

	fmt.Fprintln(w, "# HELP xray_prober_subscription_endpoints_count Number of endpoints discovered in subscription")
	fmt.Fprintln(w, "# TYPE xray_prober_subscription_endpoints_count gauge")
	fmt.Fprintf(w, "xray_prober_subscription_endpoints_count{region=%q} %d\n\n", m.region, m.endpointsCount)

	fmt.Fprintln(w, "# HELP xray_prober_subscription_last_poll_timestamp_seconds Last poll timestamp for subscription")
	fmt.Fprintln(w, "# TYPE xray_prober_subscription_last_poll_timestamp_seconds gauge")
	pollTs := int64(0)
	if !m.lastPollTime.IsZero() {
		pollTs = m.lastPollTime.Unix()
	}
	fmt.Fprintf(w, "xray_prober_subscription_last_poll_timestamp_seconds{region=%q} %d\n\n", m.region, pollTs)

	fmt.Fprintln(w, "# HELP xray_prober_subscription_poll_errors_total Total errors encountered when polling subscription")
	fmt.Fprintln(w, "# TYPE xray_prober_subscription_poll_errors_total counter")
	fmt.Fprintf(w, "xray_prober_subscription_poll_errors_total{region=%q} %d\n\n", m.region, m.pollErrors)

	// Endpoint status (up)
	fmt.Fprintln(w, "# HELP xray_prober_endpoint_up Endpoint availability status per target (1 = reachable, 0 = unreachable)")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_up gauge")
	m.forEachEndpointTarget(func(labels map[string]string, tm *TargetProbeMetrics) {
		up := 0
		if tm != nil {
			up = tm.Up
		}
		fmt.Fprintf(w, "xray_prober_endpoint_up{%s} %d\n", formatLabels(labels), up)
	})
	fmt.Fprintln(w)

	// Endpoint latency gauge
	fmt.Fprintln(w, "# HELP xray_prober_endpoint_latency_seconds Endpoint total latency in seconds")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_latency_seconds gauge")
	m.forEachEndpointTarget(func(labels map[string]string, tm *TargetProbeMetrics) {
		var lat float64
		if tm != nil {
			lat = tm.LatencySec
		}
		fmt.Fprintf(w, "xray_prober_endpoint_latency_seconds{%s} %g\n", formatLabels(labels), lat)
	})
	fmt.Fprintln(w)

	// Phase metrics: TTFB, DNS, TCP, TLS
	fmt.Fprintln(w, "# HELP xray_prober_endpoint_ttfb_seconds Time to First Byte (TTFB) in seconds")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_ttfb_seconds gauge")
	m.forEachEndpointTarget(func(labels map[string]string, tm *TargetProbeMetrics) {
		var val float64
		if tm != nil {
			val = tm.Phases.TTFBDurationSec
		}
		fmt.Fprintf(w, "xray_prober_endpoint_ttfb_seconds{%s} %g\n", formatLabels(labels), val)
	})
	fmt.Fprintln(w)

	fmt.Fprintln(w, "# HELP xray_prober_endpoint_dns_seconds DNS resolution time in seconds")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_dns_seconds gauge")
	m.forEachEndpointTarget(func(labels map[string]string, tm *TargetProbeMetrics) {
		var val float64
		if tm != nil {
			val = tm.Phases.DNSDurationSec
		}
		fmt.Fprintf(w, "xray_prober_endpoint_dns_seconds{%s} %g\n", formatLabels(labels), val)
	})
	fmt.Fprintln(w)

	fmt.Fprintln(w, "# HELP xray_prober_endpoint_tcp_connect_seconds TCP connect time through proxy in seconds")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_tcp_connect_seconds gauge")
	m.forEachEndpointTarget(func(labels map[string]string, tm *TargetProbeMetrics) {
		var val float64
		if tm != nil {
			val = tm.Phases.TCPDurationSec
		}
		fmt.Fprintf(w, "xray_prober_endpoint_tcp_connect_seconds{%s} %g\n", formatLabels(labels), val)
	})
	fmt.Fprintln(w)

	fmt.Fprintln(w, "# HELP xray_prober_endpoint_tls_handshake_seconds TLS handshake time in seconds")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_tls_handshake_seconds gauge")
	m.forEachEndpointTarget(func(labels map[string]string, tm *TargetProbeMetrics) {
		var val float64
		if tm != nil {
			val = tm.Phases.TLSDurationSec
		}
		fmt.Fprintf(w, "xray_prober_endpoint_tls_handshake_seconds{%s} %g\n", formatLabels(labels), val)
	})
	fmt.Fprintln(w)

	// Consecutive failures
	fmt.Fprintln(w, "# HELP xray_prober_endpoint_consecutive_failures Current consecutive probe failures count")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_consecutive_failures gauge")
	m.forEachEndpointTarget(func(labels map[string]string, tm *TargetProbeMetrics) {
		var errs int64
		if tm != nil {
			errs = tm.ConsecutiveErr
		}
		fmt.Fprintf(w, "xray_prober_endpoint_consecutive_failures{%s} %d\n", formatLabels(labels), errs)
	})
	fmt.Fprintln(w)

	// Cumulative Errors
	fmt.Fprintln(w, "# HELP xray_prober_endpoint_probe_errors_total Cumulative probe errors count")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_probe_errors_total counter")
	m.forEachEndpointTarget(func(labels map[string]string, tm *TargetProbeMetrics) {
		var errs int64
		if tm != nil {
			errs = tm.ErrorsCount
		}
		fmt.Fprintf(w, "xray_prober_endpoint_probe_errors_total{%s} %d\n", formatLabels(labels), errs)
	})
	fmt.Fprintln(w)

	// Latency Histogram (for percentiles: histogram_quantile(0.95, sum(rate(..._bucket[5m])) by (le, endpoint)))
	fmt.Fprintln(w, "# HELP xray_prober_endpoint_http_duration_seconds HTTP request duration histogram for percentile calculations")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_http_duration_seconds histogram")
	m.forEachEndpointTarget(func(labels map[string]string, tm *TargetProbeMetrics) {
		if tm == nil || tm.Histogram == nil {
			return
		}
		// Buckets
		for _, b := range LatencyBuckets {
			bucketLabels := copyMap(labels)
			bucketLabels["le"] = fmt.Sprintf("%g", b)
			fmt.Fprintf(w, "xray_prober_endpoint_http_duration_seconds_bucket{%s} %d\n",
				formatLabels(bucketLabels), tm.Histogram.Buckets[b])
		}
		infLabels := copyMap(labels)
		infLabels["le"] = "+Inf"
		fmt.Fprintf(w, "xray_prober_endpoint_http_duration_seconds_bucket{%s} %d\n",
			formatLabels(infLabels), tm.Histogram.Count)

		fmt.Fprintf(w, "xray_prober_endpoint_http_duration_seconds_sum{%s} %g\n",
			formatLabels(labels), tm.Histogram.Sum)
		fmt.Fprintf(w, "xray_prober_endpoint_http_duration_seconds_count{%s} %d\n",
			formatLabels(labels), tm.Histogram.Count)
	})
	fmt.Fprintln(w)

	// Speedtest metrics
	fmt.Fprintln(w, "# HELP xray_prober_endpoint_speed_bytes_per_second Download speed in bytes per second")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_speed_bytes_per_second gauge")
	writeSortedMeta(w, m.endpointMeta, func(id string, labels string) {
		var bps float64
		if met := m.endpointMetrics[id]; met != nil {
			bps = met.SpeedBps
		}
		fmt.Fprintf(w, "xray_prober_endpoint_speed_bytes_per_second{%s} %g\n", labels, bps)
	})
	fmt.Fprintln(w)

	fmt.Fprintln(w, "# HELP xray_prober_endpoint_speed_duration_seconds Duration of the last speedtest probe")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_speed_duration_seconds gauge")
	writeSortedMeta(w, m.endpointMeta, func(id string, labels string) {
		var dur float64
		if met := m.endpointMetrics[id]; met != nil {
			dur = met.SpeedDurSec
		}
		fmt.Fprintf(w, "xray_prober_endpoint_speed_duration_seconds{%s} %g\n", labels, dur)
	})
	fmt.Fprintln(w)
}

func (m *Metrics) forEachEndpointTarget(fn func(labels map[string]string, tm *TargetProbeMetrics)) {
	ids := make([]string, 0, len(m.endpointMeta))
	for id := range m.endpointMeta {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		meta := m.endpointMeta[id]
		epMet := m.endpointMetrics[id]
		if epMet == nil || len(epMet.Targets) == 0 {
			// Emit default target label if no target probe yet
			labels := copyMap(meta)
			labels["target"] = "default"
			fn(labels, nil)
			continue
		}

		targetTags := make([]string, 0, len(epMet.Targets))
		for t := range epMet.Targets {
			targetTags = append(targetTags, t)
		}
		sort.Strings(targetTags)

		for _, t := range targetTags {
			labels := copyMap(meta)
			labels["target"] = t
			fn(labels, epMet.Targets[t])
		}
	}
}

func copyMap(m map[string]string) map[string]string {
	res := make(map[string]string, len(m))
	for k, v := range m {
		res[k] = v
	}
	return res
}

func writeSortedMeta(w io.Writer, meta map[string]map[string]string, fn func(id, labels string)) {
	ids := make([]string, 0, len(meta))
	for id := range meta {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		labels := formatLabels(meta[id])
		fn(id, labels)
	}
}

func formatLabels(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		val := escapeString(labels[k])
		parts = append(parts, fmt.Sprintf("%s=%q", k, val))
	}
	return strings.Join(parts, ",")
}

func escapeString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}
