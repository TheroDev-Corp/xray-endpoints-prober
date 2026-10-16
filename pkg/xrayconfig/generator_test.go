package xrayconfig

import (
	"encoding/json"
	"testing"

	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/subscription"
)

func TestGenerateConfig(t *testing.T) {
	endpoints := []subscription.Endpoint{
		{
			ID:          "ep-1",
			Name:        "TestVless",
			Protocol:    "vless",
			Server:      "vless.example.com",
			Port:        443,
			UUID:        "11111111-1111-1111-1111-111111111111",
			Security:    "reality",
			PublicKey:   "fake-key",
			ShortID:     "fake-sid",
			Fingerprint: "chrome",
		},
		{
			ID:       "ep-2",
			Name:     "TestTrojan",
			Protocol: "trojan",
			Server:   "trojan.example.com",
			Port:     443,
			Password: "trojan-password",
			Security: "tls",
			SNI:      "trojan.example.com",
			Network:  "ws",
			Path:     "/ws",
		},
		{
			ID:       "ep-3",
			Name:     "TestVMess",
			Protocol: "vmess",
			Server:   "vmess.example.com",
			Port:     8443,
			UUID:     "22222222-2222-2222-2222-222222222222",
			Security: "tls",
			Network:  "tcp",
		},
		{
			ID:       "ep-4",
			Name:     "TestSS",
			Protocol: "shadowsocks",
			Server:   "ss.example.com",
			Port:     8388,
			Method:   "aes-128-gcm",
			Password: "sspassword",
		},
	}

	basePort := 10800
	cfg, assigned, err := GenerateConfig(endpoints, basePort)
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	if len(cfg.Inbounds) != 4 {
		t.Errorf("expected 4 inbounds, got %d", len(cfg.Inbounds))
	}
	if len(cfg.Outbounds) != 5 { // 4 endpoints + direct
		t.Errorf("expected 5 outbounds, got %d", len(cfg.Outbounds))
	}
	if len(cfg.Routing.Rules) != 4 {
		t.Errorf("expected 4 routing rules, got %d", len(cfg.Routing.Rules))
	}

	for i, ep := range assigned {
		expectedPort := basePort + i
		if ep.LocalPort != expectedPort {
			t.Errorf("expected local port %d for ep %d, got %d", expectedPort, i, ep.LocalPort)
		}
		if cfg.Inbounds[i].Port != expectedPort {
			t.Errorf("expected inbound port %d for inbound %d, got %d", expectedPort, i, cfg.Inbounds[i].Port)
		}
		if cfg.Inbounds[i].Tag != cfg.Routing.Rules[i].InboundTag[0] {
			t.Errorf("mismatch inbound tag in routing rule")
		}
		if cfg.Outbounds[i].Tag != cfg.Routing.Rules[i].OutboundTag {
			t.Errorf("mismatch outbound tag in routing rule")
		}
	}

	// Verify JSON marshaling
	jsonBytes, err := ToJSON(cfg)
	if err != nil {
		t.Fatalf("failed to marshal to JSON: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
		t.Fatalf("generated invalid json: %v", err)
	}
}

func TestGenerateConfigXHTTPAndTrojanReality(t *testing.T) {
	endpoints := []subscription.Endpoint{
		{
			ID:          "ep-xhttp",
			Name:        "TestXHTTP",
			Protocol:    "vless",
			Server:      "xhttp.example.com",
			Port:        443,
			UUID:        "11111111-1111-1111-1111-111111111111",
			Security:    "reality",
			PublicKey:   "fake-key",
			ShortID:     "fake-sid",
			Network:     "xhttp",
			Path:        "/path",
			Host:        "host.example.com",
			Mode:        "auto",
		},
		{
			ID:          "ep-trojan-reality",
			Name:        "TestTrojanReality",
			Protocol:    "trojan",
			Server:      "trojan.example.com",
			Port:        443,
			Password:    "password",
			Security:    "reality",
			PublicKey:   "fake-key",
			ShortID:     "fake-sid",
			Network:     "tcp",
		},
	}

	cfg, _, err := GenerateConfig(endpoints, 10800)
	if err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	// Verify xhttp settings
	ob0 := cfg.Outbounds[0]
	if ob0.StreamSettings.Network != "xhttp" {
		t.Errorf("expected network xhttp, got %s", ob0.StreamSettings.Network)
	}
	if ob0.StreamSettings.XHTTPSettings == nil {
		t.Fatal("expected non-nil XHTTPSettings")
	}
	if ob0.StreamSettings.XHTTPSettings["path"] != "/path" {
		t.Errorf("expected /path, got %v", ob0.StreamSettings.XHTTPSettings["path"])
	}
	if ob0.StreamSettings.XHTTPSettings["mode"] != "auto" {
		t.Errorf("expected mode auto, got %v", ob0.StreamSettings.XHTTPSettings["mode"])
	}

	// Verify trojan reality settings
	ob1 := cfg.Outbounds[1]
	if ob1.StreamSettings.Security != "reality" {
		t.Errorf("expected security reality, got %s", ob1.StreamSettings.Security)
	}
	if ob1.StreamSettings.RealitySettings == nil {
		t.Fatal("expected non-nil RealitySettings for trojan")
	}
	if ob1.StreamSettings.RealitySettings["publicKey"] != "fake-key" {
		t.Errorf("expected fake-key, got %s", ob1.StreamSettings.RealitySettings["publicKey"])
	}
}
