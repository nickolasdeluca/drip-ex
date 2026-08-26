//go:build windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"drip/pkg/config"
)

// seedUserConfig writes a config with one tunnel and points DRIP_CONFIG at it.
func seedUserConfig(t *testing.T, tunnelName string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("DRIP_CONFIG", path)

	cfg := &config.ClientConfig{
		Server: "tunnel.example.com:443",
		Token:  "drip_abc_def",
		TLS:    true,
		Tunnels: []*config.TunnelConfig{
			{Name: tunnelName, Type: "http", Port: 9765},
		},
	}
	if err := config.SaveClientConfig(cfg, path); err != nil {
		t.Fatalf("SaveClientConfig() error = %v", err)
	}
	return path
}

func TestPrepareServiceConfigSeedsFromTheUserConfig(t *testing.T) {
	source := seedUserConfig(t, "web")
	target := filepath.Join(t.TempDir(), "machine", "config.yaml")

	path, seededFrom, err := prepareServiceConfig(target, false)
	if err != nil {
		t.Fatalf("prepareServiceConfig() error = %v", err)
	}
	if path != target {
		t.Fatalf("path = %q, want %q", path, target)
	}
	if seededFrom != source {
		t.Fatalf("seededFrom = %q, want %q", seededFrom, source)
	}

	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read copy: %v", err)
	}
	if !strings.Contains(string(written), "web") {
		t.Fatalf("copy = %q, want the tunnel from the user config", written)
	}
}

// An existing copy is kept as-is: an administrator may have curated it, and
// silently overwriting it would discard their edits.
func TestPrepareServiceConfigKeepsAnExistingCopy(t *testing.T) {
	seedUserConfig(t, "web")

	target := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(target, []byte("server: old.example.com:443\n"), 0o600); err != nil {
		t.Fatalf("write existing copy: %v", err)
	}

	_, seededFrom, err := prepareServiceConfig(target, false)
	if err != nil {
		t.Fatalf("prepareServiceConfig() error = %v", err)
	}
	if seededFrom != "" {
		t.Fatalf("seededFrom = %q, want empty when the copy is reused", seededFrom)
	}

	kept, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read copy: %v", err)
	}
	if !strings.Contains(string(kept), "old.example.com") {
		t.Fatalf("copy = %q, want it left alone", kept)
	}
}

// --reseed is the way out of a stale copy: an earlier install can have left one
// behind from before the tunnels were configured.
func TestPrepareServiceConfigReseedOverwrites(t *testing.T) {
	source := seedUserConfig(t, "api-fas")

	target := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(target, []byte("server: old.example.com:443\n"), 0o600); err != nil {
		t.Fatalf("write stale copy: %v", err)
	}

	_, seededFrom, err := prepareServiceConfig(target, true)
	if err != nil {
		t.Fatalf("prepareServiceConfig() error = %v", err)
	}
	if seededFrom != source {
		t.Fatalf("seededFrom = %q, want %q", seededFrom, source)
	}

	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read copy: %v", err)
	}
	if !strings.Contains(string(written), "api-fas") {
		t.Fatalf("copy = %q, want the refreshed user config", written)
	}
	if strings.Contains(string(written), "old.example.com") {
		t.Fatalf("copy = %q, want the stale contents gone", written)
	}
}
