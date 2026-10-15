package xrayconfig

import (
	"encoding/json"
	"fmt"

	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/subscription"
)

// XrayConfig matches Xray JSON structure
type XrayConfig struct {
	Log       LogConfig        `json:"log"`
	Inbounds  []InboundConfig  `json:"inbounds"`
	Outbounds []OutboundConfig `json:"outbounds"`
	Routing   RoutingConfig    `json:"routing"`
}

type LogConfig struct {
	LogLevel string `json:"loglevel"`
}

type InboundConfig struct {
	Tag      string         `json:"tag"`
	Port     int            `json:"port"`
	Listen   string         `json:"listen"`
	Protocol string         `json:"protocol"`
	Settings map[string]any `json:"settings,omitempty"`
}

type OutboundConfig struct {
	Tag            string         `json:"tag"`
	Protocol       string         `json:"protocol"`
	Settings       map[string]any `json:"settings"`
	StreamSettings *StreamConfig  `json:"streamSettings,omitempty"`
	Mux            map[string]any `json:"mux,omitempty"`
}

type StreamConfig struct {
	Network         string         `json:"network,omitempty"`
	Security        string         `json:"security,omitempty"`
	TLSSettings     map[string]any `json:"tlsSettings,omitempty"`
	RealitySettings map[string]any `json:"realitySettings,omitempty"`
	WSSettings      map[string]any `json:"wsSettings,omitempty"`
	GRPCSettings    map[string]any `json:"grpcSettings,omitempty"`
	HTTPSettings    map[string]any `json:"httpSettings,omitempty"`
	XHTTPSettings   map[string]any `json:"xhttpSettings,omitempty"`
}

type RoutingConfig struct {
	DomainStrategy string        `json:"domainStrategy,omitempty"`
	Rules          []RoutingRule `json:"rules"`
}

type RoutingRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	OutboundTag string   `json:"outboundTag"`
}

// GenerateConfig creates XrayConfig and assigns local ports to endpoints
func GenerateConfig(endpoints []subscription.Endpoint, basePort int) (*XrayConfig, []subscription.Endpoint, error) {
	cfg := &XrayConfig{
		Log: LogConfig{
			LogLevel: "error",
		},
		Inbounds:  make([]InboundConfig, 0, len(endpoints)),
		Outbounds: make([]OutboundConfig, 0, len(endpoints)+1),
		Routing: RoutingConfig{
			DomainStrategy: "AsIs",
			Rules:          make([]RoutingRule, 0, len(endpoints)+1),
		},
	}

	assignedEndpoints := make([]subscription.Endpoint, len(endpoints))

	for i, ep := range endpoints {
		localPort := basePort + i
		inboundTag := fmt.Sprintf("in-%d", localPort)
		outboundTag := fmt.Sprintf("out-%d", localPort)

		epCopy := ep
		epCopy.LocalPort = localPort
		assignedEndpoints[i] = epCopy

		// Inbound: SOCKS5/HTTP (Socks handles both in modern Xray or socks inbound)
		inbound := InboundConfig{
			Tag:      inboundTag,
			Port:     localPort,
			Listen:   "127.0.0.1",
			Protocol: "socks",
			Settings: map[string]any{
				"auth": "noauth",
				"udp":  true,
			},
		}
		cfg.Inbounds = append(cfg.Inbounds, inbound)

		// Outbound
		outbound, err := buildOutbound(epCopy, outboundTag)
		if err != nil {
			return nil, nil, fmt.Errorf("failed building outbound for %s (%s): %w", ep.Name, ep.Protocol, err)
		}
		cfg.Outbounds = append(cfg.Outbounds, outbound)

		// Routing rule: in-<port> -> out-<port>
		cfg.Routing.Rules = append(cfg.Routing.Rules, RoutingRule{
			Type:        "field",
			InboundTag:  []string{inboundTag},
			OutboundTag: outboundTag,
		})
	}

	// Fallback direct outbound
	cfg.Outbounds = append(cfg.Outbounds, OutboundConfig{
		Tag:      "direct",
		Protocol: "freedom",
		Settings: map[string]any{},
	})

	return cfg, assignedEndpoints, nil
}

func buildOutbound(ep subscription.Endpoint, tag string) (OutboundConfig, error) {
	outbound := OutboundConfig{
		Tag:      tag,
		Protocol: ep.Protocol,
		Settings: make(map[string]any),
	}

	stream := &StreamConfig{
		Network: ep.Network,
	}
	if stream.Network == "" {
		stream.Network = "tcp"
	}

	if ep.Security != "" && ep.Security != "none" {
		stream.Security = ep.Security
		if ep.Security == "tls" {
			tlsMap := map[string]any{
				"allowInsecure": false,
			}
			if ep.SNI != "" {
				tlsMap["serverName"] = ep.SNI
			} else if ep.Host != "" {
				tlsMap["serverName"] = ep.Host
			}
			if ep.Fingerprint != "" {
				tlsMap["fingerprint"] = ep.Fingerprint
			}
			stream.TLSSettings = tlsMap
		} else if ep.Security == "reality" {
			realityMap := map[string]any{
				"show":        false,
				"fingerprint": ep.Fingerprint,
				"publicKey":   ep.PublicKey,
				"shortId":     ep.ShortID,
				"spiderX":     ep.SpiderX,
			}
			if ep.SNI != "" {
				realityMap["serverName"] = ep.SNI
			}
			stream.RealitySettings = realityMap
		}
	}

	// Transport settings
	if ep.Network == "ws" {
		wsMap := map[string]any{}
		if ep.Path != "" {
			wsMap["path"] = ep.Path
		}
		headers := make(map[string]string)
		if ep.Host != "" {
			headers["Host"] = ep.Host
		}
		if len(headers) > 0 {
			wsMap["headers"] = headers
		}
		stream.WSSettings = wsMap
	} else if ep.Network == "grpc" {
		grpcMap := map[string]any{}
		if ep.Path != "" {
			grpcMap["serviceName"] = ep.Path
		}
		stream.GRPCSettings = grpcMap
	} else if ep.Network == "http" || ep.Network == "h2" {
		httpMap := map[string]any{}
		if ep.Path != "" {
			httpMap["path"] = ep.Path
		}
		if ep.Host != "" {
			httpMap["host"] = []string{ep.Host}
		}
		stream.HTTPSettings = httpMap
	} else if ep.Network == "xhttp" || ep.Network == "splithttp" {
		xhttpMap := map[string]any{}
		if ep.Path != "" {
			xhttpMap["path"] = ep.Path
		}
		if ep.Host != "" {
			xhttpMap["host"] = ep.Host
		}
		// Validate mode against supported Xray modes
		switch ep.Mode {
		case "auto", "stream-up", "stream-one", "packet-up":
			xhttpMap["mode"] = ep.Mode
		default:
			// If empty, "multi", or unsupported, fallback to "auto"
			xhttpMap["mode"] = "auto"
		}
		stream.XHTTPSettings = xhttpMap
	}

	outbound.StreamSettings = stream

	switch ep.Protocol {
	case "vless":
		userMap := map[string]any{
			"id":         ep.UUID,
			"encryption": "none",
		}
		if ep.Flow != "" {
			userMap["flow"] = ep.Flow
		}
		outbound.Settings = map[string]any{
			"vnext": []map[string]any{
				{
					"address": ep.Server,
					"port":    ep.Port,
					"users":   []map[string]any{userMap},
				},
			},
		}

	case "vmess":
		userMap := map[string]any{
			"id":       ep.UUID,
			"alterId":  0,
			"security": "auto",
		}
		outbound.Settings = map[string]any{
			"vnext": []map[string]any{
				{
					"address": ep.Server,
					"port":    ep.Port,
					"users":   []map[string]any{userMap},
				},
			},
		}

	case "trojan":
		outbound.Settings = map[string]any{
			"servers": []map[string]any{
				{
					"address":  ep.Server,
					"port":     ep.Port,
					"password": ep.Password,
				},
			},
		}

	case "shadowsocks":
		outbound.Settings = map[string]any{
			"servers": []map[string]any{
				{
					"address":  ep.Server,
					"port":     ep.Port,
					"method":   ep.Method,
					"password": ep.Password,
				},
			},
		}

	default:
		return outbound, fmt.Errorf("unsupported protocol for outbound: %s", ep.Protocol)
	}

	return outbound, nil
}

// ToJSON marshals config to formatted JSON
func ToJSON(cfg *XrayConfig) ([]byte, error) {
	return json.MarshalIndent(cfg, "", "  ")
}

// GenerateConfigBytes is a helper to return raw JSON bytes and updated endpoints
func GenerateConfigBytes(endpoints []subscription.Endpoint, basePort int) ([]byte, []subscription.Endpoint, error) {
	cfg, eps, err := GenerateConfig(endpoints, basePort)
	if err != nil {
		return nil, nil, err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal config: %w", err)
	}
	return data, eps, nil
}
