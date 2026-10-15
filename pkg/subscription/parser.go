package subscription

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ParseSubscriptionContent parses raw subscription content in various formats:
// 1. Base64 encoded list of links (or plaintext lines)
// 2. Clash JSON / YAML proxies configuration
// 3. Sing-box JSON configuration
func ParseSubscriptionContent(content []byte, excludedProtocols []string) (endpoints []Endpoint, totalCount int, excludedCount int, contentHash string, err error) {
	hash := sha256.Sum256(content)
	contentHash = hex.EncodeToString(hash[:])

	excludeMap := make(map[string]bool)
	for _, p := range excludedProtocols {
		excludeMap[strings.ToLower(strings.TrimSpace(p))] = true
	}

	// Try Sing-box JSON / Clash JSON first
	parsed, err := tryParseJSON(content)
	if err == nil && len(parsed) > 0 {
		filtered, excluded := filterEndpoints(parsed, excludeMap)
		return filtered, len(parsed), excluded, contentHash, nil
	}

	// Try Clash YAML (using simple YAML parser)
	parsed, err = tryParseClashYAML(string(content))
	if err == nil && len(parsed) > 0 {
		filtered, excluded := filterEndpoints(parsed, excludeMap)
		return filtered, len(parsed), excluded, contentHash, nil
	}

	// Try base64 decoded string
	strContent := strings.TrimSpace(string(content))
	decoded, err := tryBase64Decode(strContent)
	if err == nil {
		strContent = decoded
	}

	// Parse lines (vless://, vmess://, trojan://, ss://)
	parsed, err = parseLines(strContent)
	if err != nil {
		return nil, 0, 0, "", err
	}

	filtered, excluded := filterEndpoints(parsed, excludeMap)
	return filtered, len(parsed), excluded, contentHash, nil
}

func filterEndpoints(endpoints []Endpoint, excludeMap map[string]bool) ([]Endpoint, int) {
	var result []Endpoint
	excluded := 0
	for _, ep := range endpoints {
		normProto := strings.ToLower(strings.TrimSpace(ep.Protocol))
		if normProto == "shadowsocks" {
			normProto = "ss"
		}
		if excludeMap[normProto] || (ep.Protocol == "shadowsocks" && excludeMap["shadowsocks"]) {
			excluded++
			continue
		}
		result = append(result, ep)
	}
	return result, excluded
}

func tryBase64Decode(s string) (string, error) {
	s = strings.TrimSpace(s)
	// Remove any extra whitespace / newlines before decoding
	clean := strings.ReplaceAll(s, "\n", "")
	clean = strings.ReplaceAll(clean, "\r", "")
	clean = strings.ReplaceAll(clean, " ", "")

	// Try standard base64
	data, err := base64.StdEncoding.DecodeString(clean)
	if err == nil {
		return string(data), nil
	}
	// Try URL base64
	data, err = base64.URLEncoding.DecodeString(clean)
	if err == nil {
		return string(data), nil
	}
	// Try RawStdEncoding
	data, err = base64.RawStdEncoding.DecodeString(clean)
	if err == nil {
		return string(data), nil
	}
	// Try RawURLEncoding
	data, err = base64.RawURLEncoding.DecodeString(clean)
	if err == nil {
		return string(data), nil
	}

	return "", fmt.Errorf("not base64")
}

func parseLines(text string) ([]Endpoint, error) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	var endpoints []Endpoint
	idx := 1

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		ep, err := ParseLink(line)
		if err != nil {
			// Skip unrecognized lines
			continue
		}
		if ep.ID == "" {
			ep.ID = fmt.Sprintf("ep-%d", idx)
		}
		endpoints = append(endpoints, *ep)
		idx++
	}

	return endpoints, nil
}

func ParseLink(raw string) (*Endpoint, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "vless://") {
		return parseVLESS(raw)
	} else if strings.HasPrefix(raw, "vmess://") {
		return parseVMess(raw)
	} else if strings.HasPrefix(raw, "trojan://") {
		return parseTrojan(raw)
	} else if strings.HasPrefix(raw, "ss://") {
		return parseShadowsocks(raw)
	}
	return nil, fmt.Errorf("unsupported link format: %s", raw)
}

func parseVLESS(raw string) (*Endpoint, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}

	ep := &Endpoint{
		Protocol: "vless",
		Raw:      raw,
		Server:   u.Hostname(),
		UUID:     u.User.Username(),
		Extra:    make(map[string]string),
	}

	port, err := strconv.Atoi(u.Port())
	if err == nil {
		ep.Port = port
	}

	if u.Fragment != "" {
		ep.Name, _ = url.QueryUnescape(u.Fragment)
	}
	if ep.Name == "" {
		ep.Name = fmt.Sprintf("vless-%s-%d", ep.Server, ep.Port)
	}

	q := u.Query()
	ep.Security = q.Get("security")
	ep.Network = q.Get("type")
	if ep.Network == "" {
		ep.Network = "tcp"
	}
	ep.Path = q.Get("path")
	ep.Host = q.Get("host")
	ep.SNI = q.Get("sni")
	ep.Fingerprint = q.Get("fp")
	ep.PublicKey = q.Get("pbk")
	ep.ShortID = q.Get("sid")
	ep.SpiderX = q.Get("spx")
	ep.Flow = q.Get("flow")
	ep.Mode = q.Get("mode")
	ep.Flow = q.Get("flow")
	ep.Mode = q.Get("mode")

	for k, v := range q {
		if len(v) > 0 {
			ep.Extra[k] = v[0]
		}
	}

	return ep, nil
}

func parseTrojan(raw string) (*Endpoint, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}

	ep := &Endpoint{
		Protocol: "trojan",
		Raw:      raw,
		Server:   u.Hostname(),
		Password: u.User.Username(),
		Extra:    make(map[string]string),
	}

	port, err := strconv.Atoi(u.Port())
	if err == nil {
		ep.Port = port
	}

	if u.Fragment != "" {
		ep.Name, _ = url.QueryUnescape(u.Fragment)
	}
	if ep.Name == "" {
		ep.Name = fmt.Sprintf("trojan-%s-%d", ep.Server, ep.Port)
	}

	q := u.Query()
	ep.Security = q.Get("security")
	if ep.Security == "" {
		ep.Security = "tls"
	}
	ep.Network = q.Get("type")
	if ep.Network == "" {
		ep.Network = "tcp"
	}
	ep.Path = q.Get("path")
	ep.Host = q.Get("host")
	ep.SNI = q.Get("sni")
	ep.Fingerprint = q.Get("fp")
	ep.PublicKey = q.Get("pbk")
	ep.ShortID = q.Get("sid")
	ep.SpiderX = q.Get("spx")
	ep.Flow = q.Get("flow")
	ep.Mode = q.Get("mode")

	for k, v := range q {
		if len(v) > 0 {
			ep.Extra[k] = v[0]
		}
	}

	return ep, nil
}

func parseVMess(raw string) (*Endpoint, error) {
	b64Part := strings.TrimPrefix(raw, "vmess://")
	decoded, err := tryBase64Decode(b64Part)
	if err != nil {
		return nil, fmt.Errorf("failed to decode vmess base64: %w", err)
	}

	var data struct {
		V    any    `json:"v"`
		PS   string `json:"ps"`
		Add  string `json:"add"`
		Port any    `json:"port"`
		ID   string `json:"id"`
		Aid  any    `json:"aid"`
		Scy  string `json:"scy"`
		Net  string `json:"net"`
		Type string `json:"type"`
		Host string `json:"host"`
		Path string `json:"path"`
		Tls  string `json:"tls"`
		Sni  string `json:"sni"`
		Alpn string `json:"alpn"`
		Fp   string `json:"fp"`
	}

	if err := json.Unmarshal([]byte(decoded), &data); err != nil {
		return nil, fmt.Errorf("failed to parse vmess json: %w", err)
	}

	ep := &Endpoint{
		Protocol:    "vmess",
		Raw:         raw,
		Name:        data.PS,
		Server:      data.Add,
		UUID:        data.ID,
		Network:     data.Net,
		Path:        data.Path,
		Host:        data.Host,
		SNI:         data.Sni,
		Fingerprint: data.Fp,
		Security:    data.Tls,
		Extra:       make(map[string]string),
	}

	if ep.Security == "" && data.Tls != "" {
		ep.Security = data.Tls
	}

	switch p := data.Port.(type) {
	case float64:
		ep.Port = int(p)
	case string:
		ep.Port, _ = strconv.Atoi(p)
	}

	if ep.Network == "" {
		ep.Network = "tcp"
	}
	if ep.Name == "" {
		ep.Name = fmt.Sprintf("vmess-%s-%d", ep.Server, ep.Port)
	}

	return ep, nil
}

func parseShadowsocks(raw string) (*Endpoint, error) {
	// ss://BASE64(method:password@hostname:port)#Name
	// or ss://BASE64(method:password)@hostname:port#Name
	trimmed := strings.TrimPrefix(raw, "ss://")
	name := ""
	if idx := strings.Index(trimmed, "#"); idx != -1 {
		name, _ = url.QueryUnescape(trimmed[idx+1:])
		trimmed = trimmed[:idx]
	}

	var method, password, hostStr string

	if strings.Contains(trimmed, "@") {
		// format: base64(method:password)@hostname:port
		parts := strings.SplitN(trimmed, "@", 2)
		userInfo, err := tryBase64Decode(parts[0])
		if err != nil {
			userInfo = parts[0]
		}
		userParts := strings.SplitN(userInfo, ":", 2)
		if len(userParts) == 2 {
			method = userParts[0]
			password = userParts[1]
		}
		hostStr = parts[1]
	} else {
		// format: base64(method:password@hostname:port)
		decoded, err := tryBase64Decode(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid ss uri: %w", err)
		}
		atIdx := strings.LastIndex(decoded, "@")
		if atIdx == -1 {
			return nil, fmt.Errorf("invalid ss uri format")
		}
		userInfo := decoded[:atIdx]
		hostStr = decoded[atIdx+1:]
		userParts := strings.SplitN(userInfo, ":", 2)
		if len(userParts) == 2 {
			method = userParts[0]
			password = userParts[1]
		}
	}

	host, portStr, err := splitHostPort(hostStr)
	if err != nil {
		return nil, err
	}
	port, _ := strconv.Atoi(portStr)

	if name == "" {
		name = fmt.Sprintf("ss-%s-%d", host, port)
	}

	return &Endpoint{
		Protocol: "shadowsocks",
		Raw:      raw,
		Name:     name,
		Server:   host,
		Port:     port,
		Method:   method,
		Password: password,
		Extra:    make(map[string]string),
	}, nil
}

func splitHostPort(hp string) (string, string, error) {
	// Support both ipv6 [::1]:port and ipv4 host:port
	if strings.HasPrefix(hp, "[") {
		idx := strings.LastIndex(hp, "]:")
		if idx != -1 {
			return hp[1:idx], hp[idx+2:], nil
		}
	}
	parts := strings.Split(hp, ":")
	if len(parts) == 2 {
		return parts[0], parts[1], nil
	}
	return "", "", fmt.Errorf("invalid host:port: %s", hp)
}

// JSON parsing for Sing-box or Clash JSON
func tryParseJSON(content []byte) ([]Endpoint, error) {
	// Check sing-box outbounds
	var singBox struct {
		Outbounds []struct {
			Type       string `json:"type"`
			Tag        string `json:"tag"`
			Server     string `json:"server"`
			ServerPort int    `json:"server_port"`
			UUID       string `json:"uuid"`
			Password   string `json:"password"`
			Method     string `json:"method"`
			Network    string `json:"network"`
			Flow       string `json:"flow"`
			TLS        *struct {
				Enabled    bool   `json:"enabled"`
				ServerName string `json:"server_name"`
				Reality    *struct {
					Enabled   bool   `json:"enabled"`
					PublicKey string `json:"public_key"`
					ShortID   string `json:"short_id"`
				} `json:"reality"`
			} `json:"tls"`
			Transport *struct {
				Type string `json:"type"`
				Path string `json:"path"`
				Host string `json:"host"`
			} `json:"transport"`
		} `json:"outbounds"`
	}

	if err := json.Unmarshal(content, &singBox); err == nil && len(singBox.Outbounds) > 0 {
		var endpoints []Endpoint
		for i, ob := range singBox.Outbounds {
			proto := strings.ToLower(ob.Type)
			if proto != "vless" && proto != "vmess" && proto != "trojan" && proto != "shadowsocks" {
				continue
			}
			ep := Endpoint{
				ID:       fmt.Sprintf("ep-%d", i+1),
				Name:     ob.Tag,
				Protocol: proto,
				Server:   ob.Server,
				Port:     ob.ServerPort,
				UUID:     ob.UUID,
				Password: ob.Password,
				Method:   ob.Method,
				Flow:     ob.Flow,
				Extra:    make(map[string]string),
			}
			if ob.TLS != nil && ob.TLS.Enabled {
				ep.Security = "tls"
				ep.SNI = ob.TLS.ServerName
				if ob.TLS.Reality != nil && ob.TLS.Reality.Enabled {
					ep.Security = "reality"
					ep.PublicKey = ob.TLS.Reality.PublicKey
					ep.ShortID = ob.TLS.Reality.ShortID
				}
			}
			if ob.Transport != nil {
				ep.Network = ob.Transport.Type
				ep.Path = ob.Transport.Path
				ep.Host = ob.Transport.Host
			}
			endpoints = append(endpoints, ep)
		}
		if len(endpoints) > 0 {
			return endpoints, nil
		}
	}

	// Check Clash JSON
	var clash struct {
		Proxies []map[string]any `json:"proxies"`
	}
	if err := json.Unmarshal(content, &clash); err == nil && len(clash.Proxies) > 0 {
		return parseClashProxies(clash.Proxies), nil
	}

	return nil, fmt.Errorf("not json proxies")
}

func parseClashProxies(proxies []map[string]any) []Endpoint {
	var endpoints []Endpoint
	for i, p := range proxies {
		proto, _ := p["type"].(string)
		proto = strings.ToLower(proto)
		if proto == "ss" {
			proto = "shadowsocks"
		}
		if proto != "vless" && proto != "vmess" && proto != "trojan" && proto != "shadowsocks" {
			continue
		}

		name, _ := p["name"].(string)
		server, _ := p["server"].(string)
		var port int
		switch pt := p["port"].(type) {
		case float64:
			port = int(pt)
		case int:
			port = pt
		case string:
			port, _ = strconv.Atoi(pt)
		}

		ep := Endpoint{
			ID:       fmt.Sprintf("ep-%d", i+1),
			Name:     name,
			Protocol: proto,
			Server:   server,
			Port:     port,
			Extra:    make(map[string]string),
		}

		if uuid, ok := p["uuid"].(string); ok {
			ep.UUID = uuid
		}
		if pass, ok := p["password"].(string); ok {
			ep.Password = pass
		}
		if method, ok := p["cipher"].(string); ok {
			ep.Method = method
		}
		if sni, ok := p["sni"].(string); ok {
			ep.SNI = sni
		}
		if net, ok := p["network"].(string); ok {
			ep.Network = net
		}
		if tlsVal, ok := p["tls"].(bool); ok && tlsVal {
			ep.Security = "tls"
		}
		if ro, ok := p["reality-opts"].(map[string]any); ok {
			ep.Security = "reality"
			if pk, ok := ro["public-key"].(string); ok {
				ep.PublicKey = pk
			}
			if sid, ok := ro["short-id"].(string); ok {
				ep.ShortID = sid
			}
		}

		endpoints = append(endpoints, ep)
	}
	return endpoints
}

// A simple parser for Clash YAML proxy list (which is line-based key-values)
func tryParseClashYAML(text string) ([]Endpoint, error) {
	if !strings.Contains(text, "proxies:") {
		return nil, fmt.Errorf("no proxies section")
	}

	lines := strings.Split(text, "\n")
	var inProxies bool
	var currentMap map[string]any
	var proxies []map[string]any

	flushCurrent := func() {
		if currentMap != nil && len(currentMap) > 0 {
			proxies = append(proxies, currentMap)
			currentMap = nil
		}
	}

	reKeyVal := regexp.MustCompile(`^[\s-]*([a-zA-Z0-9_-]+)\s*:\s*(.*)$`)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "proxies:") {
			inProxies = true
			continue
		}
		if !inProxies {
			continue
		}
		if inProxies && len(line) > 0 && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, "-") {
			// Out of proxies section
			break
		}

		if strings.HasPrefix(trimmed, "- ") {
			flushCurrent()
			currentMap = make(map[string]any)
			trimmed = strings.TrimPrefix(trimmed, "- ")
		}

		if currentMap != nil {
			matches := reKeyVal.FindStringSubmatch(trimmed)
			if len(matches) == 3 {
				key := strings.TrimSpace(matches[1])
				val := strings.Trim(strings.TrimSpace(matches[2]), "\"'")
				currentMap[key] = val
			}
		}
	}
	flushCurrent()

	if len(proxies) == 0 {
		return nil, fmt.Errorf("empty clash proxies")
	}

	return parseClashProxies(proxies), nil
}
