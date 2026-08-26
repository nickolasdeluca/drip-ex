package cli

import (
	"fmt"
	"strings"

	"drip/internal/shared/ui"
	"drip/pkg/config"
	"github.com/spf13/cobra"
)

// The reservation on the server decides what a tunnel is called; the config
// here decides what it exposes. A machine running as a service needs both, and
// the tunnel half was previously only reachable by hand-editing the YAML.
var configTunnelCmd = &cobra.Command{
	Use:           "tunnel",
	Short:         "Manage the tunnels this client exposes",
	Long:          "Add, list and remove the tunnels started by 'drip start' and the Windows service",
	SilenceUsage:  true,
	SilenceErrors: true,
}

var configTunnelAddCmd = &cobra.Command{
	Use:           "add",
	Short:         "Add a tunnel to the configuration",
	Long:          "Add a tunnel definition (local port and type) to the client configuration",
	RunE:          runConfigTunnelAdd,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var configTunnelListCmd = &cobra.Command{
	Use:           "list",
	Short:         "List configured tunnels",
	RunE:          runConfigTunnelList,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var configTunnelRemoveCmd = &cobra.Command{
	Use:           "remove <name>",
	Short:         "Remove a tunnel from the configuration",
	Args:          cobra.ExactArgs(1),
	RunE:          runConfigTunnelRemove,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var (
	tunnelName      string
	tunnelType      string
	tunnelPort      int
	tunnelAddress   string
	tunnelSubdomain string
	tunnelTransport string
	tunnelBandwidth string
	tunnelReplace   bool
	tunnelNamesOnly bool
)

func init() {
	configCmd.AddCommand(configTunnelCmd)
	configTunnelCmd.AddCommand(configTunnelAddCmd)
	configTunnelCmd.AddCommand(configTunnelListCmd)
	configTunnelCmd.AddCommand(configTunnelRemoveCmd)

	configTunnelAddCmd.Flags().StringVar(&tunnelName, "name", "", "Name for this tunnel (required, unique)")
	configTunnelAddCmd.Flags().StringVar(&tunnelType, "type", "http", "Tunnel type: http, https or tcp")
	configTunnelAddCmd.Flags().IntVar(&tunnelPort, "port", 0, "Local port to expose (required)")
	configTunnelAddCmd.Flags().StringVar(&tunnelAddress, "address", "", "Local address (default: 127.0.0.1)")
	configTunnelAddCmd.Flags().StringVar(&tunnelSubdomain, "subdomain", "",
		"Request this subdomain; leave empty to take the reservation the server holds for this client")
	configTunnelAddCmd.Flags().StringVar(&tunnelTransport, "transport", "", "Transport: auto, tcp or wss")
	configTunnelAddCmd.Flags().StringVar(&tunnelBandwidth, "bandwidth", "", "Bandwidth limit, e.g. 1M")
	configTunnelAddCmd.Flags().BoolVar(&tunnelReplace, "replace", false, "Overwrite a tunnel that already has this name")

	configTunnelListCmd.Flags().BoolVar(&tunnelNamesOnly, "names", false,
		"Print one tunnel name per line and nothing else, for scripts")
}

func runConfigTunnelAdd(_ *cobra.Command, _ []string) error {
	cfg, err := loadConfigForTunnelEdit()
	if err != nil {
		return err
	}

	name := strings.TrimSpace(tunnelName)
	if name == "" {
		return fmt.Errorf("a tunnel name is required. Use --name")
	}
	if tunnelPort == 0 {
		return fmt.Errorf("a local port is required. Use --port")
	}

	tunnel := &config.TunnelConfig{
		Name:      name,
		Type:      strings.ToLower(strings.TrimSpace(tunnelType)),
		Port:      tunnelPort,
		Address:   strings.TrimSpace(tunnelAddress),
		Subdomain: strings.ToLower(strings.TrimSpace(tunnelSubdomain)),
		Transport: strings.TrimSpace(tunnelTransport),
		Bandwidth: strings.TrimSpace(tunnelBandwidth),
	}
	if err := tunnel.Validate(); err != nil {
		return err
	}

	replaced := false
	for i, existing := range cfg.Tunnels {
		if existing.Name != tunnel.Name {
			continue
		}
		if !tunnelReplace {
			return fmt.Errorf("tunnel '%s' already exists. Use --replace to overwrite it, or pick another --name", tunnel.Name)
		}
		cfg.Tunnels[i] = tunnel
		replaced = true
		break
	}
	if !replaced {
		cfg.Tunnels = append(cfg.Tunnels, tunnel)
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	if err := config.SaveClientConfig(cfg, ""); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	action := "Tunnel added: "
	if replaced {
		action = "Tunnel replaced: "
	}
	updates := []string{action + describeTunnel(tunnel)}
	if tunnel.Subdomain == "" {
		updates = append(updates, "No subdomain requested: the server assigns this client's reservation")
	}
	fmt.Println(ui.RenderConfigUpdated(updates))

	return nil
}

func runConfigTunnelList(_ *cobra.Command, _ []string) error {
	cfg, err := loadConfigForTunnelEdit()
	if err != nil {
		return err
	}

	// --names is what the installers read: bare output, no decoration, and an
	// empty result when nothing is configured.
	if tunnelNamesOnly {
		for _, t := range cfg.Tunnels {
			fmt.Println(t.Name)
		}
		return nil
	}

	if len(cfg.Tunnels) == 0 {
		fmt.Println(ui.Muted("No tunnels configured in " + config.DefaultClientConfigPath()))
		fmt.Println(ui.Muted("Add one with: drip config tunnel add --name web --type http --port 3000"))
		return nil
	}

	fmt.Println(ui.Title("Configured Tunnels"))
	for _, t := range cfg.Tunnels {
		fmt.Println("  " + describeTunnel(t))
	}

	return nil
}

func runConfigTunnelRemove(_ *cobra.Command, args []string) error {
	cfg, err := loadConfigForTunnelEdit()
	if err != nil {
		return err
	}

	name := strings.TrimSpace(args[0])
	kept := make([]*config.TunnelConfig, 0, len(cfg.Tunnels))
	for _, t := range cfg.Tunnels {
		if t.Name != name {
			kept = append(kept, t)
		}
	}
	if len(kept) == len(cfg.Tunnels) {
		return fmt.Errorf("tunnel '%s' not found. Configured tunnels: %s",
			name, strings.Join(cfg.GetTunnelNames(), ", "))
	}

	cfg.Tunnels = kept
	if err := config.SaveClientConfig(cfg, ""); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Println(ui.RenderConfigUpdated([]string{"Tunnel removed: " + name}))

	return nil
}

// loadConfigForTunnelEdit refuses to invent a config file: a tunnel with no
// server to reach is not worth writing, and LoadClientConfig already says how
// to create one.
func loadConfigForTunnelEdit() (*config.ClientConfig, error) {
	return config.LoadClientConfig("")
}

func describeTunnel(t *config.TunnelConfig) string {
	address := t.Address
	if address == "" {
		address = "127.0.0.1"
	}

	out := fmt.Sprintf("%s  %s  %s:%d", t.Name, t.Type, address, t.Port)
	if t.Subdomain != "" {
		out += "  subdomain=" + t.Subdomain
	}
	if t.Transport != "" {
		out += "  transport=" + t.Transport
	}
	if t.Bandwidth != "" {
		out += "  bandwidth=" + t.Bandwidth
	}
	return out
}
