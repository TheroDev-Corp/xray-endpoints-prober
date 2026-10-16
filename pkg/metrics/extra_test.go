package metrics

import (
	"testing"
	"time"
)

func TestMetrics_ExtraMethods(t *testing.T) {
	m := NewMetrics("test-reg", "v26.0.0")

	m.SetCoreVersion("v26.3.27")
	m.RegisterEndpoint("ep-1", "Node 1", "vless", "1.1.1.1", 443)
	m.RegisterEndpoint("ep-2", "Node 2", "vmess", "2.2.2.2", 443)
	m.RecordLatency("ep-1", true, 50*time.Millisecond, nil)

	if len(m.endpointMeta) != 2 {
		t.Fatalf("expected 2 endpoints before prune, got %d", len(m.endpointMeta))
	}

	active := map[string]bool{"ep-1": true}
	m.PruneMissing(active)

	if len(m.endpointMeta) != 1 {
		t.Errorf("expected 1 endpoint after prune, got %d", len(m.endpointMeta))
	}
	if _, ok := m.endpointMeta["ep-1"]; !ok {
		t.Errorf("expected ep-1 to be retained")
	}
}
