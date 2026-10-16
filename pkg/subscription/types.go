package subscription

// Endpoint represents a parsed proxy endpoint
type Endpoint struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Protocol    string            `json:"protocol"` // vless, vmess, trojan, shadowsocks
	Server      string            `json:"server"`
	Port        int               `json:"port"`
	UUID        string            `json:"uuid,omitempty"`
	Password    string            `json:"password,omitempty"`
	Method      string            `json:"method,omitempty"`   // cipher method for SS
	Security    string            `json:"security,omitempty"` // tls, reality, none
	Network     string            `json:"network,omitempty"`  // tcp, ws, grpc, httpupgrade
	Path        string            `json:"path,omitempty"`
	Host        string            `json:"host,omitempty"`
	SNI         string            `json:"sni,omitempty"`
	Fingerprint string            `json:"fingerprint,omitempty"`
	PublicKey   string            `json:"public_key,omitempty"` // Reality
	ShortID     string            `json:"short_id,omitempty"`   // Reality
	SpiderX     string            `json:"spider_x,omitempty"`   // Reality
	Flow        string            `json:"flow,omitempty"`       // xtls-rprx-vision
	Mode        string            `json:"mode,omitempty"`       // xhttp mode: auto, stream-one, etc.
	Raw         string            `json:"raw,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
	LocalPort   int               `json:"local_port,omitempty"` // Local SOCKS5/HTTP inbound port
}
