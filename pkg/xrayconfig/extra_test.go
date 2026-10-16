package xrayconfig

import (
	"testing"

	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/subscription"
)

func TestGenerateConfig_VariousProtocols(t *testing.T) {
	endpoints := []subscription.Endpoint{
		{
			ID:          "ep-vmess",
			Name:        "VMess Test",
			Protocol:    "vmess",
			Server:      "1.2.3.4",
			Port:        443,
			UUID:        "b3c6248c-48d7-4fe9-b77f-7fc9d26fa901",
			Security:    "tls",
			Network:     "ws",
			Path:        "/vmess-path",
			Host:        "example.com",
			SNI:         "example.com",
			Fingerprint: "chrome",
		},
		{
			ID:       "ep-ss",
			Name:     "Shadowsocks Test",
			Protocol: "shadowsocks",
			Server:   "5.6.7.8",
			Port:     8388,
			Password: "mypassword",
			Method:   "aes-256-gcm",
		},
		{
			ID:       "ep-grpc",
			Name:     "VLESS gRPC Test",
			Protocol: "vless",
			Server:   "9.10.11.12",
			Port:     443,
			UUID:     "b3c6248c-48d7-4fe9-b77f-7fc9d26fa901",
			Network:  "grpc",
			Security: "tls",
			Path:     "grpc-service",
		},
		{
			ID:       "ep-httpupgrade",
			Name:     "VLESS HTTPUpgrade Test",
			Protocol: "vless",
			Server:   "9.10.11.13",
			Port:     443,
			UUID:     "b3c6248c-48d7-4fe9-b77f-7fc9d26fa901",
			Network:  "httpupgrade",
			Security: "tls",
			Path:     "/upgrade",
		},
	}

	cfg, assigned, err := GenerateConfig(endpoints, 10800)
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	if len(assigned) != 4 {
		t.Fatalf("expected 4 assigned endpoints, got %d", len(assigned))
	}
	if len(cfg.Inbounds) != 4 {
		t.Fatalf("expected 4 inbounds, got %d", len(cfg.Inbounds))
	}
	if len(cfg.Outbounds) != 5 { // 4 endpoints + 1 direct
		t.Fatalf("expected 5 outbounds (including direct), got %d", len(cfg.Outbounds))
	}

	jsonBytes, _, err := GenerateConfigBytes(endpoints, 10800)
	if err != nil {
		t.Fatalf("GenerateConfigBytes failed: %v", err)
	}
	if len(jsonBytes) == 0 {
		t.Errorf("expected non-empty JSON bytes")
	}
}

func TestGenerateConfig_EmptyEndpoints(t *testing.T) {
	cfg, assigned, err := GenerateConfig(nil, 10800)
	if err != nil {
		t.Fatalf("unexpected error for nil endpoints: %v", err)
	}
	if len(assigned) != 0 || len(cfg.Inbounds) != 0 {
		t.Errorf("expected 0 inbounds for empty endpoints")
	}
	if len(cfg.Outbounds) != 1 { // direct
		t.Errorf("expected 1 direct outbound for empty endpoints, got %d", len(cfg.Outbounds))
	}
}
