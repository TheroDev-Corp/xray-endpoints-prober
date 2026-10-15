package metrics

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestMetricsPrometheusOutput(t *testing.T) {
	m := NewMetrics("eu-central-1", "v1.8.23")
	m.SetSubscriptionState(10, time.Unix(1700000000, 0), 2)
	m.RegisterEndpoint("ep-1", "Server 1", "vless", "1.2.3.4", 443)
	m.RecordLatency("ep-1", true, 150*time.Millisecond, nil)
	m.RecordSpeed("ep-1", 1024*1024, 2*time.Second, nil)

	var buf bytes.Buffer
	m.WritePrometheus(&buf)

	output := buf.String()

	expectedSubstrings := []string{
		`xray_prober_core_version_info{region="eu-central-1",version="v1.8.23"} 1`,
		`xray_prober_subscription_endpoints_count{region="eu-central-1"} 10`,
		`xray_prober_subscription_last_poll_timestamp_seconds{region="eu-central-1"} 1700000000`,
		`xray_prober_subscription_poll_errors_total{region="eu-central-1"} 2`,
		`xray_prober_endpoint_up{id="ep-1",name="Server 1",port="443",protocol="vless",region="eu-central-1",server="1.2.3.4"} 1`,
		`xray_prober_endpoint_latency_seconds{id="ep-1",name="Server 1",port="443",protocol="vless",region="eu-central-1",server="1.2.3.4"} 0.15`,
		`xray_prober_endpoint_speed_bytes_per_second{id="ep-1",name="Server 1",port="443",protocol="vless",region="eu-central-1",server="1.2.3.4"} 524288`,
		`xray_prober_endpoint_speed_duration_seconds{id="ep-1",name="Server 1",port="443",protocol="vless",region="eu-central-1",server="1.2.3.4"} 2`,
	}

	for _, s := range expectedSubstrings {
		if !strings.Contains(output, s) {
			t.Errorf("missing expected string: %s\nin output:\n%s", s, output)
		}
	}
}
