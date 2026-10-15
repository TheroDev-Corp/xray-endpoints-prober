package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type TargetConfig struct {
	Tag string // "cloudflare", "gstatic", "google", etc.
	URL string
}

type Config struct {
	Region                   string
	XrayVersion              string
	XrayCacheDir             string
	SubscriptionURL          string
	SubscriptionPollInterval time.Duration
	SubscriptionCacheFile    string
	ProtocolsExclude         []string

	// Targets for probing latency / availability
	ProbeTargets           []TargetConfig
	ProbePingInterval      time.Duration
	ProbePingTimeout       time.Duration

	// Speedtest settings
	ProbeSpeedtestURL      string
	ProbeSpeedtestInterval time.Duration
	ProbeSpeedtestTimeout  time.Duration

	ProbeConcurrency int
	ListenAddr       string
	InboundBasePort  int
}

func Load() (*Config, error) {
	defaultTargets := "https://cp.cloudflare.com/generate_204,https://www.gstatic.com/generate_204,https://www.google.com/generate_204"

	// Backward compatibility: check PROBE_TARGET_URLS first, fallback to PROBE_PING_URL
	rawTargets := getEnv("PROBE_TARGET_URLS", "")
	if rawTargets == "" {
		if pingURL := getEnv("PROBE_PING_URL", ""); pingURL != "" {
			rawTargets = pingURL
		} else {
			rawTargets = defaultTargets
		}
	}

	targets := parseTargetURLs(rawTargets)
	if len(targets) == 0 {
		targets = parseTargetURLs(defaultTargets)
	}

	cfg := &Config{
		Region:                   getEnv("REGION", "default"),
		XrayVersion:              getEnv("XRAY_VERSION", "v24.11.30"),
		XrayCacheDir:             getEnv("XRAY_CACHE_DIR", "/tmp/xray-cache"),
		SubscriptionURL:          getEnv("SUBSCRIPTION_URL", ""),
		SubscriptionPollInterval: getEnvDuration("SUBSCRIPTION_POLL_INTERVAL", 10*time.Minute),
		SubscriptionCacheFile:    getEnv("SUBSCRIPTION_CACHE_FILE", "/tmp/subscription.cache"),
		ProtocolsExclude:         parseCommaList(getEnv("PROTOCOLS_EXCLUDE", "")),
		ProbeTargets:             targets,
		ProbePingInterval:        getEnvDuration("PROBE_PING_INTERVAL", 30*time.Second),
		ProbePingTimeout:         getEnvDuration("PROBE_PING_TIMEOUT", 5*time.Second),
		ProbeSpeedtestURL:        getEnv("PROBE_SPEEDTEST_URL", "https://speed.cloudflare.com/__down?bytes=10000000"),
		ProbeSpeedtestInterval:   getEnvDuration("PROBE_SPEEDTEST_INTERVAL", 10*time.Minute),
		ProbeSpeedtestTimeout:    getEnvDuration("PROBE_SPEEDTEST_TIMEOUT", 15*time.Second),
		ProbeConcurrency:         getEnvInt("PROBE_CONCURRENCY", 10),
		ListenAddr:               getEnv("LISTEN_ADDR", ":8080"),
		InboundBasePort:          getEnvInt("INBOUND_BASE_PORT", 10800),
	}

	if cfg.ProbeConcurrency <= 0 {
		return nil, fmt.Errorf("PROBE_CONCURRENCY must be > 0, got %d", cfg.ProbeConcurrency)
	}
	if cfg.ProbePingInterval <= 0 {
		return nil, fmt.Errorf("PROBE_PING_INTERVAL must be > 0, got %v", cfg.ProbePingInterval)
	}
	if cfg.ProbePingTimeout <= 0 {
		return nil, fmt.Errorf("PROBE_PING_TIMEOUT must be > 0, got %v", cfg.ProbePingTimeout)
	}
	if cfg.InboundBasePort <= 1024 || cfg.InboundBasePort > 60000 {
		cfg.InboundBasePort = 10800
	}

	return cfg, nil
}

func parseTargetURLs(s string) []TargetConfig {
	parts := strings.Split(s, ",")
	var res []TargetConfig
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		tag := extractTag(trimmed)
		res = append(res, TargetConfig{
			Tag: tag,
			URL: trimmed,
		})
	}
	return res
}

func extractTag(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "target"
	}
	host := strings.ToLower(u.Hostname())
	// Strip common prefixes
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "cp.")
	// Map well known domains to clean tags
	if strings.Contains(host, "cloudflare") {
		return "cloudflare"
	}
	if strings.Contains(host, "gstatic") {
		return "gstatic"
	}
	if strings.Contains(host, "google") {
		return "google"
	}
	if strings.Contains(host, "rutube") {
		return "rutube"
	}
	if strings.Contains(host, "vk") {
		return "vk"
	}
	if strings.Contains(host, "yandex") {
		return "yandex"
	}
	return host
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return defaultVal
}

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	valStr, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(valStr) == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(strings.TrimSpace(valStr))
	if err != nil {
		if sec, errSec := strconv.Atoi(strings.TrimSpace(valStr)); errSec == nil {
			return time.Duration(sec) * time.Second
		}
		return defaultVal
	}
	return d
}

func getEnvInt(key string, defaultVal int) int {
	valStr, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(valStr) == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(strings.TrimSpace(valStr))
	if err != nil {
		return defaultVal
	}
	return n
}

func parseCommaList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var res []string
	for _, p := range parts {
		trimmed := strings.ToLower(strings.TrimSpace(p))
		if trimmed != "" {
			res = append(res, trimmed)
		}
	}
	return res
}
