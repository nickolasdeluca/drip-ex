package tls

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"

	"drip/internal/server/tls/hostinger"
)

// DNSProviderConfig is the credential material a DNS provider needs to answer
// the ACME DNS-01 challenge.
type DNSProviderConfig struct {
	// Name selects the provider, e.g. "cloudflare".
	Name string
	// APIToken is the provider's scoped API token. Cloudflare needs a token
	// with Zone.DNS:Write on the zone hosting the tunnel domain; Hostinger
	// needs an hPanel API token with DNS access to it.
	APIToken string
}

// dnsProviderFactory builds a certmagic DNS provider from credentials.
type dnsProviderFactory func(cfg DNSProviderConfig) (certmagic.DNSProvider, error)

// dnsProviderEntry is what the registry stores for one provider.
type dnsProviderEntry struct {
	// newProvider builds the libdns provider from credentials.
	newProvider dnsProviderFactory

	// propagationDelay is how long to wait after writing a challenge record
	// before checking whether it is visible, when the operator sets no value.
	//
	// It exists because the server asks for two certificates — the domain and
	// the wildcard — whose DNS-01 challenges land on the same
	// _acme-challenge name in separate, sequential orders. The CA caches the
	// first order's answer for the record's TTL, so unless the second
	// validation happens after that cache expires, the CA checks the second
	// token against the first one and rejects it. A delay longer than the
	// shortest TTL the provider allows is what separates them.
	propagationDelay time.Duration
}

// dnsProviders is the registry of supported DNS providers. Adding another
// libdns provider is a new import plus one entry here; the provider only has to
// satisfy libdns.RecordAppender and libdns.RecordDeleter.
var dnsProviders = map[string]dnsProviderEntry{
	"cloudflare": {
		newProvider: func(cfg DNSProviderConfig) (certmagic.DNSProvider, error) {
			if cfg.APIToken == "" {
				return nil, fmt.Errorf("cloudflare needs an API token with Zone.DNS:Write")
			}
			return &cloudflare.Provider{APIToken: cfg.APIToken}, nil
		},
	},
	"hostinger": {
		newProvider: func(cfg DNSProviderConfig) (certmagic.DNSProvider, error) {
			if cfg.APIToken == "" {
				return nil, fmt.Errorf("hostinger needs an API token with DNS access to the zone")
			}
			return &hostinger.Provider{APIToken: cfg.APIToken}, nil
		},
		// The API refuses any TTL below 60 seconds, so a challenge record is
		// cacheable for at least that long. 90 clears it with room to spare.
		propagationDelay: 90 * time.Second,
	},
}

// DefaultPropagationDelay reports the wait a provider needs between writing a
// challenge record and checking for it, or zero when it needs none.
func DefaultPropagationDelay(name string) time.Duration {
	entry, ok := dnsProviders[normalizeProviderName(name)]
	if !ok {
		return 0
	}
	return entry.propagationDelay
}

// SupportedDNSProviders lists the registered provider names, sorted.
func SupportedDNSProviders() []string {
	names := make([]string, 0, len(dnsProviders))
	for name := range dnsProviders {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// newDNSProvider resolves a provider by name.
func newDNSProvider(cfg DNSProviderConfig) (certmagic.DNSProvider, error) {
	name := normalizeProviderName(cfg.Name)
	if name == "" {
		return nil, fmt.Errorf("a DNS provider is required for ACME wildcard certificates: %s",
			strings.Join(SupportedDNSProviders(), ", "))
	}

	entry, ok := dnsProviders[name]
	if !ok {
		return nil, fmt.Errorf("unsupported DNS provider %q: want one of %s",
			cfg.Name, strings.Join(SupportedDNSProviders(), ", "))
	}

	cfg.Name = name
	return entry.newProvider(cfg)
}

// normalizeProviderName renders a configured provider name the way the registry
// keys it.
func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
