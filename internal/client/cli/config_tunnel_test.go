package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"drip/pkg/config"
)

// seedClientConfig points DRIP_CONFIG at a throwaway file holding a usable
// client configuration, and returns its path.
func seedClientConfig(t *testing.T, tunnels ...*config.TunnelConfig) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("DRIP_CONFIG", path)

	cfg := &config.ClientConfig{
		Server:  "tunnel.example.com:443",
		Token:   "drip_abc_def",
		TLS:     true,
		Tunnels: tunnels,
	}
	if err := config.SaveClientConfig(cfg, path); err != nil {
		t.Fatalf("SaveClientConfig() error = %v", err)
	}
	return path
}

// resetTunnelFlags clears the command's package-level flag state, which cobra
// keeps between runs within one process.
func resetTunnelFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		tunnelName, tunnelType, tunnelPort = "", "http", 0
		tunnelAddress, tunnelSubdomain, tunnelTransport, tunnelBandwidth = "", "", "", ""
		tunnelReplace, tunnelNamesOnly = false, false
	})
	tunnelName, tunnelType, tunnelPort = "", "http", 0
	tunnelAddress, tunnelSubdomain, tunnelTransport, tunnelBandwidth = "", "", "", ""
	tunnelReplace, tunnelNamesOnly = false, false
}

func loadTunnels(t *testing.T, path string) []*config.TunnelConfig {
	t.Helper()

	cfg, err := config.LoadClientConfig(path)
	if err != nil {
		t.Fatalf("LoadClientConfig() error = %v", err)
	}
	return cfg.Tunnels
}

func TestConfigTunnelAddWritesTheTunnel(t *testing.T) {
	path := seedClientConfig(t)
	resetTunnelFlags(t)

	tunnelName, tunnelType, tunnelPort = "web", "http", 9765

	if err := runConfigTunnelAdd(nil, nil); err != nil {
		t.Fatalf("runConfigTunnelAdd() error = %v", err)
	}

	tunnels := loadTunnels(t, path)
	if len(tunnels) != 1 {
		t.Fatalf("config holds %d tunnels, want 1", len(tunnels))
	}
	if tunnels[0].Name != "web" || tunnels[0].Type != "http" || tunnels[0].Port != 9765 {
		t.Fatalf("tunnel = %+v, want web/http/9765", tunnels[0])
	}
	// No subdomain is the point: the server binds the client's reservation.
	if tunnels[0].Subdomain != "" {
		t.Errorf("Subdomain = %q, want it empty", tunnels[0].Subdomain)
	}
}

func TestConfigTunnelAddRequiresNameAndPort(t *testing.T) {
	seedClientConfig(t)
	resetTunnelFlags(t)

	tunnelPort = 9765
	if err := runConfigTunnelAdd(nil, nil); err == nil {
		t.Fatal("runConfigTunnelAdd() with no name succeeded, want an error")
	}

	tunnelName, tunnelPort = "web", 0
	if err := runConfigTunnelAdd(nil, nil); err == nil {
		t.Fatal("runConfigTunnelAdd() with no port succeeded, want an error")
	}
}

func TestConfigTunnelAddRejectsAnInvalidTunnel(t *testing.T) {
	seedClientConfig(t)
	resetTunnelFlags(t)

	tunnelName, tunnelType, tunnelPort = "web", "ftp", 21
	err := runConfigTunnelAdd(nil, nil)
	if err == nil {
		t.Fatal("runConfigTunnelAdd() with type ftp succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "http, https, or tcp") {
		t.Errorf("error = %v, want it to name the valid types", err)
	}
}

// A duplicate name is a mistake worth stopping, since the second one would
// otherwise shadow the first at startup.
func TestConfigTunnelAddRefusesDuplicateName(t *testing.T) {
	path := seedClientConfig(t, &config.TunnelConfig{Name: "web", Type: "http", Port: 3000})
	resetTunnelFlags(t)

	tunnelName, tunnelType, tunnelPort = "web", "http", 9765
	if err := runConfigTunnelAdd(nil, nil); err == nil {
		t.Fatal("runConfigTunnelAdd() with a taken name succeeded, want an error")
	}

	if got := loadTunnels(t, path)[0].Port; got != 3000 {
		t.Fatalf("port = %d, want the original 3000 left alone", got)
	}
}

func TestConfigTunnelAddReplaces(t *testing.T) {
	path := seedClientConfig(t, &config.TunnelConfig{Name: "web", Type: "http", Port: 3000})
	resetTunnelFlags(t)

	tunnelName, tunnelType, tunnelPort, tunnelReplace = "web", "http", 9765, true
	if err := runConfigTunnelAdd(nil, nil); err != nil {
		t.Fatalf("runConfigTunnelAdd() error = %v", err)
	}

	tunnels := loadTunnels(t, path)
	if len(tunnels) != 1 {
		t.Fatalf("config holds %d tunnels, want 1", len(tunnels))
	}
	if tunnels[0].Port != 9765 {
		t.Fatalf("port = %d, want 9765", tunnels[0].Port)
	}
}

func TestConfigTunnelRemove(t *testing.T) {
	path := seedClientConfig(t,
		&config.TunnelConfig{Name: "web", Type: "http", Port: 3000},
		&config.TunnelConfig{Name: "db", Type: "tcp", Port: 5432},
	)
	resetTunnelFlags(t)

	if err := runConfigTunnelRemove(nil, []string{"web"}); err != nil {
		t.Fatalf("runConfigTunnelRemove() error = %v", err)
	}

	tunnels := loadTunnels(t, path)
	if len(tunnels) != 1 || tunnels[0].Name != "db" {
		t.Fatalf("tunnels = %+v, want only db", tunnels)
	}
}

func TestConfigTunnelRemoveUnknownNameLists(t *testing.T) {
	seedClientConfig(t, &config.TunnelConfig{Name: "web", Type: "http", Port: 3000})
	resetTunnelFlags(t)

	err := runConfigTunnelRemove(nil, []string{"nope"})
	if err == nil {
		t.Fatal("runConfigTunnelRemove() for a missing tunnel succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "web") {
		t.Errorf("error = %v, want it to list the configured tunnels", err)
	}
}

// The commands refuse to write a config file that has no server to reach.
func TestConfigTunnelNeedsAnExistingConfig(t *testing.T) {
	t.Setenv("DRIP_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	resetTunnelFlags(t)

	tunnelName, tunnelPort = "web", 9765
	if err := runConfigTunnelAdd(nil, nil); err == nil {
		t.Fatal("runConfigTunnelAdd() with no config file succeeded, want an error")
	}
	if err := runConfigTunnelList(nil, nil); err == nil {
		t.Fatal("runConfigTunnelList() with no config file succeeded, want an error")
	}
}
