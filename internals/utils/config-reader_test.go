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
		{"cf no zones", func(c *Config) {
			c.CF.CFZoneID = ""
			c.CF.CFDNSEntries = ""
		}, "CF.CF_ZONES"},
		{"cf multi zone", func(c *Config) {
			c.CF.CFZoneID, c.CF.CFDNSEntries = "", ""
			c.CF.CFZones = "z1:r1;z2:r2"
		}, ""},
		{"cf malformed zones", func(c *Config) { c.CF.CFZones = "z1" }, "CF.CF_ZONES"},
		{"cf default zone id", func(c *Config) { c.CF.CFZones = "your-zone-id:r1" }, "CF.CF_ZONES"},
		{"cf override for unknown zone", func(c *Config) { c.CF.CFZoneTokens = "other:tok" }, "zone other is not configured"},
		{"cf token optional when every zone has an override", func(c *Config) {
			c.CF.CFToken = ""
			c.CF.CFZoneTokens = "zone:tok"
		}, ""},
		{"cf disabled ignores cf fields", func(c *Config) {
			c.GoCI.EnableCloudflare = false
			c.CF = CFConfig{}
		}, ""},
		{"ionos missing key", func(c *Config) { c.IONOS.IONOSApiKey = "" }, "IONOS_API_KEY"},
		{"ionos multi zone", func(c *Config) { c.IONOS.IONOSZones = "z2:r2" }, ""},
		{"ionos malformed key override", func(c *Config) { c.IONOS.IONOSZoneApiKeys = "zone" }, "IONOS.IONOS_ZONE_API_KEYS"},
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
	want := []ZoneEntry{{ZoneID: "zone", RecordIDs: []string{"r1", "r2"}, Credential: "token"}}
	if !reflect.DeepEqual(cfg.CF.Zones, want) {
		t.Errorf("CF.Zones = %+v, want %+v", cfg.CF.Zones, want)
	}
}

func TestLoadConfigMultiZone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.ini")
	content := `[GoCI]
DOMAIN_TO_RESOLVE = home.example.org
ENABLE_CLOUDFLARE = true
ENABLE_IONOS = true

[CF]
CF_TOKEN = token
CF_ZONES = z1:r1, r2; z2:r3
CF_ZONE_TOKENS = z2:other-token
CF_ZONE_ID = z1
CF_DNS_ENTRIES_ID = r2,r4

[IONOS]
IONOS_API_URL = https://api.hosting.ionos.com/dns
IONOS_API_KEY = prefix.secret
IONOS_ZONES = i1:a;i2:b,c
IONOS_ZONE_API_KEYS = i1:prefix2.secret2
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// The legacy zone z1 is merged into the CF_ZONES entry, without duplicates.
	wantCF := []ZoneEntry{
		{ZoneID: "z1", RecordIDs: []string{"r1", "r2", "r4"}, Credential: "token"},
		{ZoneID: "z2", RecordIDs: []string{"r3"}, Credential: "other-token"},
	}
	if !reflect.DeepEqual(cfg.CF.Zones, wantCF) {
		t.Errorf("CF.Zones = %+v, want %+v", cfg.CF.Zones, wantCF)
	}
	wantIONOS := []ZoneEntry{
		{ZoneID: "i1", RecordIDs: []string{"a"}, Credential: "prefix2.secret2"},
		{ZoneID: "i2", RecordIDs: []string{"b", "c"}, Credential: "prefix.secret"},
	}
	if !reflect.DeepEqual(cfg.IONOS.Zones, wantIONOS) {
		t.Errorf("IONOS.Zones = %+v, want %+v", cfg.IONOS.Zones, wantIONOS)
	}
}

func TestLoadConfigRejectsSpaceBeforeSemicolon(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.ini")
	content := "[GoCI]\nDOMAIN_TO_RESOLVE = home.example.org\nENABLE_CLOUDFLARE = true\n\n[CF]\nCF_TOKEN = token\nCF_ZONES = z1:r1 ; z2:r2\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "CF_ZONES") {
		t.Fatalf("err = %v, want a CF_ZONES separator error", err)
	}
}

func TestParseZones(t *testing.T) {
	tests := []struct {
		raw     string
		want    []ZoneEntry
		wantErr bool
	}{
		{"", nil, false},
		{" ; ", nil, false},
		{"z1:r1", []ZoneEntry{{ZoneID: "z1", RecordIDs: []string{"r1"}}}, false},
		{"z1:r1,r2;z2:r3;", []ZoneEntry{
			{ZoneID: "z1", RecordIDs: []string{"r1", "r2"}},
			{ZoneID: "z2", RecordIDs: []string{"r3"}},
		}, false},
		{"z1", nil, true},
		{":r1", nil, true},
		{"z1:", nil, true},
		{"z1:r1,z2:r2", nil, false}, // "z2:r2" is just an odd record ID; ValidateConfig cannot tell
	}
	for _, tt := range tests {
		got, err := parseZones(tt.raw)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseZones(%q) err = %v, wantErr %v", tt.raw, err, tt.wantErr)
			continue
		}
		if tt.want != nil && !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseZones(%q) = %+v, want %+v", tt.raw, got, tt.want)
		}
	}
}

func TestParseZoneCredentialsDoesNotLeakSecrets(t *testing.T) {
	_, err := parseZoneCredentials("z1 secret-token")
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("err = %v, want an error without the secret", err)
	}
	if _, err := parseZoneCredentials("z1:a;z1:b"); err == nil {
		t.Fatal("expected error for duplicated zone")
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
