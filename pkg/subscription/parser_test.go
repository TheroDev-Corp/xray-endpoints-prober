package subscription

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestParseVLESS(t *testing.T) {
	vlessURL := "vless://11111111-2222-3333-4444-555555555555@example.com:443?encryption=none&security=reality&sni=example.com&fp=chrome&pbk=fake-public-key&sid=fake-short-id&spx=%2F&type=tcp&flow=xtls-rprx-vision#MyVlessNode"

	ep, err := ParseLink(vlessURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ep.Protocol != "vless" {
		t.Errorf("expected protocol vless, got %s", ep.Protocol)
	}
	if ep.Server != "example.com" {
		t.Errorf("expected server example.com, got %s", ep.Server)
	}
	if ep.Port != 443 {
		t.Errorf("expected port 443, got %d", ep.Port)
	}
	if ep.UUID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("expected uuid 11111111-2222-3333-4444-555555555555, got %s", ep.UUID)
	}
	if ep.Security != "reality" {
		t.Errorf("expected security reality, got %s", ep.Security)
	}
	if ep.PublicKey != "fake-public-key" {
		t.Errorf("expected pbk fake-public-key, got %s", ep.PublicKey)
	}
	if ep.Name != "MyVlessNode" {
		t.Errorf("expected name MyVlessNode, got %s", ep.Name)
	}
	if ep.Flow != "xtls-rprx-vision" {
		t.Errorf("expected flow xtls-rprx-vision, got %s", ep.Flow)
	}
}

func TestParseTrojan(t *testing.T) {
	trojanURL := "trojan://secret-password@trojan.example.com:443?security=tls&sni=trojan.example.com&type=ws&path=%2Fws#TrojanNode"

	ep, err := ParseLink(trojanURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ep.Protocol != "trojan" {
		t.Errorf("expected protocol trojan, got %s", ep.Protocol)
	}
	if ep.Server != "trojan.example.com" {
		t.Errorf("expected server trojan.example.com, got %s", ep.Server)
	}
	if ep.Port != 443 {
		t.Errorf("expected port 443, got %d", ep.Port)
	}
	if ep.Password != "secret-password" {
		t.Errorf("expected password secret-password, got %s", ep.Password)
	}
	if ep.Network != "ws" {
		t.Errorf("expected network ws, got %s", ep.Network)
	}
	if ep.Path != "/ws" {
		t.Errorf("expected path /ws, got %s", ep.Path)
	}
	if ep.Name != "TrojanNode" {
		t.Errorf("expected name TrojanNode, got %s", ep.Name)
	}
}

func TestParseVMess(t *testing.T) {
	vmessJSON := `{"v":"2","ps":"VMessNode","add":"vmess.example.com","port":8443,"id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","aid":0,"scy":"auto","net":"ws","type":"none","host":"vmess.example.com","path":"/vmess","tls":"tls","sni":"vmess.example.com"}`
	encoded := base64.StdEncoding.EncodeToString([]byte(vmessJSON))
	vmessURL := "vmess://" + encoded

	ep, err := ParseLink(vmessURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ep.Protocol != "vmess" {
		t.Errorf("expected protocol vmess, got %s", ep.Protocol)
	}
	if ep.Server != "vmess.example.com" {
		t.Errorf("expected server vmess.example.com, got %s", ep.Server)
	}
	if ep.Port != 8443 {
		t.Errorf("expected port 8443, got %d", ep.Port)
	}
	if ep.UUID != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("expected uuid, got %s", ep.UUID)
	}
	if ep.Security != "tls" {
		t.Errorf("expected security tls, got %s", ep.Security)
	}
	if ep.Path != "/vmess" {
		t.Errorf("expected path /vmess, got %s", ep.Path)
	}
}

func TestParseShadowsocks(t *testing.T) {
	// ss://BASE64(aes-256-gcm:pass)@ss.example.com:8388#SSNode
	userInfo := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:mypassword"))
	ssURL := "ss://" + userInfo + "@ss.example.com:8388#SSNode"

	ep, err := ParseLink(ssURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ep.Protocol != "shadowsocks" {
		t.Errorf("expected protocol shadowsocks, got %s", ep.Protocol)
	}
	if ep.Server != "ss.example.com" {
		t.Errorf("expected server ss.example.com, got %s", ep.Server)
	}
	if ep.Port != 8388 {
		t.Errorf("expected port 8388, got %d", ep.Port)
	}
	if ep.Method != "aes-256-gcm" {
		t.Errorf("expected method aes-256-gcm, got %s", ep.Method)
	}
	if ep.Password != "mypassword" {
		t.Errorf("expected password mypassword, got %s", ep.Password)
	}
	if ep.Name != "SSNode" {
		t.Errorf("expected name SSNode, got %s", ep.Name)
	}
}

func TestParseSubscriptionBase64(t *testing.T) {
	lines := "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp#Node1\n" +
		"trojan://secret@trojan.example.com:443#Node2\n" +
		"ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:pw")) + "@ss.example.com:8388#Node3\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(lines))

	endpoints, _, _, hash, err := ParseSubscriptionContent([]byte(encoded), nil)
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	if len(endpoints) != 3 {
		t.Fatalf("expected 3 endpoints, got %d", len(endpoints))
	}
	if hash == "" {
		t.Fatalf("expected non-empty hash")
	}

	// Test protocol exclusion
	filtered, _, _, _, err := ParseSubscriptionContent([]byte(encoded), []string{"trojan", "ss"})
	if err != nil {
		t.Fatalf("failed to parse with exclude: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 endpoint after exclusion, got %d", len(filtered))
	}
	if filtered[0].Protocol != "vless" {
		t.Errorf("expected vless endpoint, got %s", filtered[0].Protocol)
	}
}

func TestParseClashYAML(t *testing.T) {
	yamlData := `
proxies:
  - name: "Clash Vless"
    type: vless
    server: vless.clash.com
    port: 443
    uuid: 12345678-1234-1234-1234-123456789abc
    tls: true
  - name: "Clash SS"
    type: ss
    server: ss.clash.com
    port: 8388
    cipher: chacha20-ietf-poly1305
    password: clashpassword
`

	endpoints, _, _, _, err := ParseSubscriptionContent([]byte(yamlData), nil)
	if err != nil {
		t.Fatalf("failed to parse clash yaml: %v", err)
	}

	if len(endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(endpoints))
	}
	if endpoints[0].Name != "Clash Vless" || endpoints[0].Server != "vless.clash.com" {
		t.Errorf("unexpected endpoint 0: %+v", endpoints[0])
	}
	if endpoints[1].Protocol != "shadowsocks" || endpoints[1].Password != "clashpassword" {
		t.Errorf("unexpected endpoint 1: %+v", endpoints[1])
	}
}

func TestCacheFallback(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sub-cache-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cacheFile := filepath.Join(tmpDir, "sub.txt")
	lines := "trojan://pass@trojan.com:443#CachedTrojan\n"
	if err := os.WriteFile(cacheFile, []byte(lines), 0644); err != nil {
		t.Fatal(err)
	}

	// Read and parse
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatal(err)
	}
	eps, _, _, hash, err := ParseSubscriptionContent(data, nil)
	if err != nil {
		t.Fatalf("failed to parse cache content: %v", err)
	}
	if len(eps) != 1 || eps[0].Name != "CachedTrojan" {
		t.Fatalf("unexpected parsed endpoints: %+v", eps)
	}
	if hash == "" {
		t.Fatalf("expected hash")
	}
}

func TestParseTrojanRealityAndXHTTP(t *testing.T) {
	trojanURL := "trojan://secret-pass@msk-relay.example.com:443?security=reality&sni=static.example.com&type=xhttp&path=%2Fstatic&mode=multi&fp=firefox&pbk=fake-pubkey&sid=fake-shortid&spx=%2F#TrojanRealityXHTTP"

	ep, err := ParseLink(trojanURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ep.Protocol != "trojan" {
		t.Errorf("expected protocol trojan, got %s", ep.Protocol)
	}
	if ep.Security != "reality" {
		t.Errorf("expected security reality, got %s", ep.Security)
	}
	if ep.Network != "xhttp" {
		t.Errorf("expected network xhttp, got %s", ep.Network)
	}
	if ep.Mode != "multi" {
		t.Errorf("expected mode multi, got %s", ep.Mode)
	}
	if ep.PublicKey != "fake-pubkey" {
		t.Errorf("expected pbk fake-pubkey, got %s", ep.PublicKey)
	}
	if ep.ShortID != "fake-shortid" {
		t.Errorf("expected sid fake-shortid, got %s", ep.ShortID)
	}
	if ep.SpiderX != "/" {
		t.Errorf("expected spx /, got %s", ep.SpiderX)
	}
}
