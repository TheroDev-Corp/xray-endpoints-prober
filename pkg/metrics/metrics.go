package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

type EndpointMetrics struct {
	Up          int     // 1 or 0
	LatencySec  float64 // Latency in seconds
	SpeedBps    float64 // Speed in bytes per second
	SpeedDurSec float64 // Speedtest duration in seconds
	ProbeErrors int64   // Errors count
	LastProbe   time.Time
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
		m.endpointMetrics[id] = &EndpointMetrics{}
	}
}

func (m *Metrics) RecordLatency(id string, up bool, latency time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	met, exists := m.endpointMetrics[id]
	if !exists {
		met = &EndpointMetrics{}
		m.endpointMetrics[id] = met
	}

	met.LastProbe = time.Now()
	if up && err == nil {
		met.Up = 1
		met.LatencySec = latency.Seconds()
	} else {
		met.Up = 0
		met.LatencySec = 0
		met.ProbeErrors++
	}
}

func (m *Metrics) RecordSpeed(id string, bytesRead int64, duration time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	met, exists := m.endpointMetrics[id]
	if !exists {
		met = &EndpointMetrics{}
		m.endpointMetrics[id] = met
	}

	if err != nil || duration <= 0 {
		met.ProbeErrors++
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

// WritePrometheus writes exposition format output
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

	fmt.Fprintln(w, "# HELP xray_prober_endpoint_up Endpoint availability status (1 = reachable, 0 = unreachable)")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_up gauge")
	writeSorted(w, m.endpointMeta, func(id string, labels string) {
		up := 0
		if met := m.endpointMetrics[id]; met != nil {
			up = met.Up
		}
		fmt.Fprintf(w, "xray_prober_endpoint_up{%s} %d\n", labels, up)
	})
	fmt.Fprintln(w)

	fmt.Fprintln(w, "# HELP xray_prober_endpoint_latency_seconds Endpoint RTT/TTFB latency in seconds")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_latency_seconds gauge")
	writeSorted(w, m.endpointMeta, func(id string, labels string) {
		var lat float64
		if met := m.endpointMetrics[id]; met != nil {
			lat = met.LatencySec
		}
		fmt.Fprintf(w, "xray_prober_endpoint_latency_seconds{%s} %g\n", labels, lat)
	})
	fmt.Fprintln(w)

	fmt.Fprintln(w, "# HELP xray_prober_endpoint_speed_bytes_per_second Download speed in bytes per second")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_speed_bytes_per_second gauge")
	writeSorted(w, m.endpointMeta, func(id string, labels string) {
		var bps float64
		if met := m.endpointMetrics[id]; met != nil {
			bps = met.SpeedBps
		}
		fmt.Fprintf(w, "xray_prober_endpoint_speed_bytes_per_second{%s} %g\n", labels, bps)
	})
	fmt.Fprintln(w)

	fmt.Fprintln(w, "# HELP xray_prober_endpoint_speed_duration_seconds Duration of the last speedtest probe")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_speed_duration_seconds gauge")
	writeSorted(w, m.endpointMeta, func(id string, labels string) {
		var dur float64
		if met := m.endpointMetrics[id]; met != nil {
			dur = met.SpeedDurSec
		}
		fmt.Fprintf(w, "xray_prober_endpoint_speed_duration_seconds{%s} %g\n", labels, dur)
	})
	fmt.Fprintln(w)

	fmt.Fprintln(w, "# HELP xray_prober_endpoint_probe_errors_total Cumulative probe errors for this endpoint")
	fmt.Fprintln(w, "# TYPE xray_prober_endpoint_probe_errors_total counter")
	writeSorted(w, m.endpointMeta, func(id string, labels string) {
		var errs int64
		if met := m.endpointMetrics[id]; met != nil {
			errs = met.ProbeErrors
		}
		fmt.Fprintf(w, "xray_prober_endpoint_probe_errors_total{%s} %d\n", labels, errs)
	})
	fmt.Fprintln(w)
}

func writeSorted(w io.Writer, meta map[string]map[string]string, fn func(id, labels string)) {
	ids := make([]string, 0, len(meta))
	for id := range meta {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		m := meta[id]
		labels := formatLabels(m)
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
