package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	os.Clearenv()
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Region != "default" {
		t.Errorf("expected Region default, got %s", cfg.Region)
	}
	if cfg.XrayVersion != "v24.11.30" {
		t.Errorf("expected XrayVersion v24.11.30, got %s", cfg.XrayVersion)
	}
	if cfg.ProbeConcurrency != 10 {
		t.Errorf("expected Concurrency 10, got %d", cfg.ProbeConcurrency)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("expected ListenAddr :8080, got %s", cfg.ListenAddr)
	}
	if len(cfg.ProbeTargets) != 3 {
		t.Errorf("expected 3 default targets, got %d", len(cfg.ProbeTargets))
	}
	if cfg.ProbeTargets[0].Tag != "cloudflare" || cfg.ProbeTargets[1].Tag != "gstatic" || cfg.ProbeTargets[2].Tag != "google" {
		t.Errorf("unexpected target tags: %+v", cfg.ProbeTargets)
	}
	if !cfg.ProbeSpeedtestEnabled {
		t.Errorf("expected ProbeSpeedtestEnabled true by default, got false")
	}
}

func TestLoadCustomEnv(t *testing.T) {
	os.Clearenv()
	os.Setenv("REGION", "eu-central")
	os.Setenv("XRAY_VERSION", "v1.8.24")
	os.Setenv("XRAY_CACHE_DIR", "/var/cache/xray")
	os.Setenv("SUBSCRIPTION_URL", "https://example.com/sub")
	os.Setenv("SUBSCRIPTION_POLL_INTERVAL", "5m")
	os.Setenv("SUBSCRIPTION_CACHE_FILE", "/var/cache/sub.txt")
	os.Setenv("PROTOCOLS_EXCLUDE", "vmess, ss ")
	os.Setenv("PROBE_TARGET_URLS", "https://1.1.1.1,https://google.com")
	os.Setenv("PROBE_PING_INTERVAL", "15s")
	os.Setenv("PROBE_PING_TIMEOUT", "2s")
	os.Setenv("PROBE_SPEEDTEST_URL", "https://speed.cloudflare.com/__down?bytes=1000000")
	os.Setenv("PROBE_SPEEDTEST_ENABLED", "false")
	os.Setenv("PROBE_SPEEDTEST_INTERVAL", "1h")
	os.Setenv("PROBE_SPEEDTEST_TIMEOUT", "20s")
	os.Setenv("PROBE_CONCURRENCY", "5")
	os.Setenv("LISTEN_ADDR", ":9090")
	os.Setenv("INBOUND_BASE_PORT", "12000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Region != "eu-central" {
		t.Errorf("expected Region eu-central, got %s", cfg.Region)
	}
	if cfg.XrayVersion != "v1.8.24" {
		t.Errorf("expected XrayVersion v1.8.24, got %s", cfg.XrayVersion)
	}
	if cfg.XrayCacheDir != "/var/cache/xray" {
		t.Errorf("expected XrayCacheDir /var/cache/xray, got %s", cfg.XrayCacheDir)
	}
	if cfg.SubscriptionURL != "https://example.com/sub" {
		t.Errorf("expected SubscriptionURL, got %s", cfg.SubscriptionURL)
	}
	if cfg.SubscriptionPollInterval != 5*time.Minute {
		t.Errorf("expected 5m, got %v", cfg.SubscriptionPollInterval)
	}
	if len(cfg.ProtocolsExclude) != 2 || cfg.ProtocolsExclude[0] != "vmess" || cfg.ProtocolsExclude[1] != "ss" {
		t.Errorf("expected [vmess, ss], got %v", cfg.ProtocolsExclude)
	}
	if len(cfg.ProbeTargets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(cfg.ProbeTargets))
	}
	if cfg.ProbeTargets[1].Tag != "google" {
		t.Errorf("expected target tag google, got %s", cfg.ProbeTargets[1].Tag)
	}
	if cfg.ProbePingInterval != 15*time.Second {
		t.Errorf("expected 15s, got %v", cfg.ProbePingInterval)
	}
	if cfg.ProbeConcurrency != 5 {
		t.Errorf("expected Concurrency 5, got %d", cfg.ProbeConcurrency)
	}
	if cfg.InboundBasePort != 12000 {
		t.Errorf("expected InboundBasePort 12000, got %d", cfg.InboundBasePort)
	}
	if cfg.ProbeSpeedtestEnabled {
		t.Errorf("expected ProbeSpeedtestEnabled false, got true")
	}
}

func TestProbeSpeedtestEnabledParsing(t *testing.T) {
	testCases := []struct {
		envVal   string
		expected bool
	}{
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{"enable", true},
		{"enabled", true},
		{"false", false},
		{"FALSE", false},
		{"0", false},
		{"no", false},
		{"disable", false},
		{"disabled", false},
	}

	for _, tc := range testCases {
		os.Clearenv()
		os.Setenv("PROBE_SPEEDTEST_ENABLED", tc.envVal)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected load error for %s: %v", tc.envVal, err)
		}
		if cfg.ProbeSpeedtestEnabled != tc.expected {
			t.Errorf("for PROBE_SPEEDTEST_ENABLED=%q, expected %v, got %v", tc.envVal, tc.expected, cfg.ProbeSpeedtestEnabled)
		}
	}
}

func TestValidation(t *testing.T) {
	os.Clearenv()
	os.Setenv("PROBE_CONCURRENCY", "-1")
	_, err := Load()
	if err == nil {
		t.Error("expected error for invalid PROBE_CONCURRENCY, got nil")
	}
}
