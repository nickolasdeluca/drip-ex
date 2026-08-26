package config

import "testing"

func TestNormalizeServerAddress(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"tunnel.example.com", "tunnel.example.com:443"},
		{"tunnel.example.com:8443", "tunnel.example.com:8443"},
		{"  tunnel.example.com  ", "tunnel.example.com:443"},
		{"tunnel.example.com:", "tunnel.example.com:443"},
		{"1.2.3.4", "1.2.3.4:443"},
		{"1.2.3.4:8443", "1.2.3.4:8443"},
		{"::1", "[::1]:443"},
		{"[fd00::1]", "[fd00::1]:443"},
		{"[fd00::1]:8443", "[fd00::1]:8443"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := NormalizeServerAddress(tt.in); got != tt.want {
			t.Errorf("NormalizeServerAddress(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestValidateDefaultsPort(t *testing.T) {
	cfg := &ClientConfig{Server: "tunnel.example.com", Token: "t", TLS: true}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.Server != "tunnel.example.com:443" {
		t.Errorf("Server = %q, want tunnel.example.com:443", cfg.Server)
	}
}
