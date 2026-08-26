package protocol

import json "github.com/goccy/go-json"

type PoolCapabilities struct {
	MaxDataConns int `json:"max_data_conns"`
	Version      int `json:"version"`
}

type IPAccessControl struct {
	AllowIPs []string `json:"allow_ips,omitempty"`
	DenyIPs  []string `json:"deny_ips,omitempty"`
}

type ProxyAuth struct {
	Enabled  bool   `json:"enabled"`
	Type     string `json:"type,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
}

type RegisterRequest struct {
	Token            string            `json:"token"`
	CustomSubdomain  string            `json:"custom_subdomain"`
	TunnelType       TunnelType        `json:"tunnel_type"`
	LocalPort        int               `json:"local_port"`
	ConnectionType   string            `json:"connection_type,omitempty"`
	TunnelID         string            `json:"tunnel_id,omitempty"`
	PoolCapabilities *PoolCapabilities `json:"pool_capabilities,omitempty"`
	IPAccess         *IPAccessControl  `json:"ip_access,omitempty"`
	ProxyAuth        *ProxyAuth        `json:"proxy_auth,omitempty"`
	Bandwidth        int64             `json:"bandwidth,omitempty"`
	// SupportsControl advertises that this client will open a control stream
	// and act on what the server sends over it.
	SupportsControl bool `json:"supports_control,omitempty"`
}

type RegisterResponse struct {
	Subdomain        string `json:"subdomain"`
	Port             int    `json:"port,omitempty"`
	URL              string `json:"url"`
	Message          string `json:"message"`
	TunnelID         string `json:"tunnel_id,omitempty"`
	SupportsDataConn bool   `json:"supports_data_conn,omitempty"`
	RecommendedConns int    `json:"recommended_conns,omitempty"`
	Bandwidth        int64  `json:"bandwidth,omitempty"`
	// SupportsControl tells the client the server will accept a control
	// stream. Old servers leave it false and the client opens nothing.
	SupportsControl bool `json:"supports_control,omitempty"`
}

// RebindRequest asks a connected client to register under a different name.
// The client applies it by reconnecting: the name it holds now was resolved
// before whatever changed on the server, and only registration can move it.
//
// An empty Subdomain means "ask for nothing", which lands the client on
// whatever reservation the server resolves for it.
type RebindRequest struct {
	Subdomain string `json:"subdomain"`
	Reason    string `json:"reason,omitempty"`
}

type DataConnectRequest struct {
	TunnelID     string `json:"tunnel_id"`
	Token        string `json:"token"`
	ConnectionID string `json:"connection_id"`
}

type DataConnectResponse struct {
	Accepted     bool   `json:"accepted"`
	ConnectionID string `json:"connection_id"`
	Message      string `json:"message,omitempty"`
}

type ErrorMessage struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func MarshalJSON(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

func UnmarshalJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
