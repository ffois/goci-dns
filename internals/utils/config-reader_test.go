package utils

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseDNSEntryIDs(t *testing.T) {
	tests := []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"id1", []string{"id1"}},
		{"id1,id2", []string{"id1", "id2"}},
		{" id1 , ,id2, ", []string{"id1", "id2"}},
	}
	for _, tt := range tests {
		if got := parseDNSEntryIDs(tt.raw); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseDNSEntryIDs(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

// validConfig returns a config with both providers enabled and valid values.
func validConfig() *Config {
	return &Config{
		GoCI: GoCIConfig{
			DomainToResolve:  "home.example.org",
			EnableCloudflare: true,
			EnableIONOS:      true,
		},
		CF: CFConfig{
			CFToken:      "token",
			CFZoneID:     "zone",
			CFDNSEntries: "r1,r2",
		},
		IONOS: IONOSConfig{
			IONOSApiUrl:  "https://api.hosting.ionos.com/dns",
			IONOSApiKey:  "key",
			IONOSZoneID:  "zone",
			IONOSEntries: "r1",
		},
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *Config)
		wantErr string // empty = valid
	}{
		{"valid", func(c *Config) {}, ""},
		{"no provider", func(c *Config) {
			c.GoCI.EnableCloudflare = false
			c.GoCI.EnableIONOS = false
		}, "at least one provider"},
		{"negative ttl", func(c *Config) { c.GoCI.TTLMinutesCheckDNSEntries = -1 }, "TTL_MINUTES_CHECK_DNS_ENTRIES"},
		{"default domain", func(c *Config) { c.GoCI.DomainToResolve = "your-domain" }, "DOMAIN_TO_RESOLVE"},
		{"override without ip", func(c *Config) { c.GoCI.OverrideNewIP = true }, "NEW_IP"},
		{"override with ip", func(c *Config) {
			c.GoCI.OverrideNewIP = true
			c.GoCI.NewIP = "1.2.3.4"
		}, ""},
		{"discovery without domain", func(c *Config) {
			c.GoCI.DomainToResolve = ""
			c.GoCI.UseIPDiscovery = true
			c.GoCI.IPDiscoveryURL = DefaultIPDiscoveryURL
		}, ""},
		{"discovery invalid url", func(c *Config) {
			c.GoCI.UseIPDiscovery = true
			c.GoCI.IPDiscoveryURL = "api.ipify.org"
		}, "IP_DISCOVERY_URL"},
		{"cf default token", func(c *Config) { c.CF.CFToken = "your-cloudflare-api-token" }, "CF_TOKEN"},
		{"cf account id optional", func(c *Config) { c.CF.CFAccountID = "" }, ""},
		{"cf no record ids", func(c *Config) { c.CF.CFDNSEntries = " , " }, "CF_DNS_ENTRIES_ID"},
		{"cf disabled ignores cf fields", func(c *Config) {
			c.GoCI.EnableCloudflare = false
			c.CF = CFConfig{}
		}, ""},
		{"ionos missing key", func(c *Config) { c.IONOS.IONOSApiKey = "" }, "IONOS_API_KEY"},
		{"ionos disabled ignores ionos fields", func(c *Config) {
			c.GoCI.EnableIONOS = false
			c.IONOS = IONOSConfig{}
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			tt.mutate(c)
			valid, err := ValidateConfig(c)
			if tt.wantErr == "" {
				if !valid || err != nil {
					t.Fatalf("expected valid config, got err=%v", err)
				}
				return
			}
			if valid || err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got valid=%v err=%v", tt.wantErr, valid, err)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.ini")
	content := `[GoCI]
TTL_MINUTES_CHECK_DNS_ENTRIES = 5
DOMAIN_TO_RESOLVE = home.example.org
ENABLE_CLOUDFLARE = true
ENABLE_IONOS = false

[CF]
CF_TOKEN = token
CF_ZONE_ID = zone
CF_DNS_ENTRIES_ID = r1, r2
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.GoCI.TTLMinutesCheckDNSEntries != 5 {
		t.Errorf("TTL = %d, want 5", cfg.GoCI.TTLMinutesCheckDNSEntries)
	}
	if want := []string{"r1", "r2"}; !reflect.DeepEqual(cfg.CF.CFDNSEntryIDs, want) {
		t.Errorf("CFDNSEntryIDs = %v, want %v", cfg.CF.CFDNSEntryIDs, want)
	}
}

func TestLoadConfigDiscoveryDefaultURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.ini")
	content := `[GoCI]
USE_IP_DISCOVERY = true
IP_DISCOVERY_URL =
ENABLE_CLOUDFLARE = true

[CF]
CF_TOKEN = token
CF_ZONE_ID = zone
CF_DNS_ENTRIES_ID = r1
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.GoCI.IPDiscoveryURL != DefaultIPDiscoveryURL {
		t.Errorf("IPDiscoveryURL = %q, want %q", cfg.GoCI.IPDiscoveryURL, DefaultIPDiscoveryURL)
	}
}

func TestLoadConfigNoProviderReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.ini")
	content := "[GoCI]\nDOMAIN_TO_RESOLVE = home.example.org\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error when no provider is enabled")
	}
}
