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
	os.Setenv("PROBE_PING_URL", "https://1.1.1.1")
	os.Setenv("PROBE_PING_INTERVAL", "15s")
	os.Setenv("PROBE_PING_TIMEOUT", "2s")
	os.Setenv("PROBE_SPEEDTEST_URL", "https://speed.cloudflare.com/__down?bytes=1000000")
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
	if cfg.ProbePingInterval != 15*time.Second {
		t.Errorf("expected 15s, got %v", cfg.ProbePingInterval)
	}
	if cfg.ProbeConcurrency != 5 {
		t.Errorf("expected Concurrency 5, got %d", cfg.ProbeConcurrency)
	}
	if cfg.InboundBasePort != 12000 {
		t.Errorf("expected InboundBasePort 12000, got %d", cfg.InboundBasePort)
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
