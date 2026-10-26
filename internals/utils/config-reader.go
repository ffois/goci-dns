package utils

import (
	"fmt"
	"goci-dns/internals/logger"
	"net/url"
	"os"
	"slices"
	"strings"

	"gopkg.in/ini.v1"
)

// DefaultIPDiscoveryURL is used when USE_IP_DISCOVERY=true and IP_DISCOVERY_URL is empty.
const DefaultIPDiscoveryURL = "https://api.ipify.org"

// DefaultIONOSApiURL is the base URL of the IONOS DNS API.
const DefaultIONOSApiURL = "https://api.hosting.ionos.com/dns"

// Config represents the structure of config.ini.
type Config struct {
	GoCI  GoCIConfig  `ini:"GoCI"`
	CF    CFConfig    `ini:"CF"`
	IONOS IONOSConfig `ini:"IONOS"`
}

// GoCIConfig maps the [GoCI] section.
type GoCIConfig struct {
	TTLMinutesCheckDNSEntries int    `ini:"TTL_MINUTES_CHECK_DNS_ENTRIES"`
	IgnoreOldIP               bool   `ini:"IGNORE_OLD_IP"`
	DomainToResolve           string `ini:"DOMAIN_TO_RESOLVE"`
	UseIPDiscovery            bool   `ini:"USE_IP_DISCOVERY"`
	IPDiscoveryURL            string `ini:"IP_DISCOVERY_URL"`
	OverrideNewIP             bool   `ini:"OVERRIDE_NEW_IP"`
	NewIP                     string `ini:"NEW_IP"`
	EnableIONOS               bool   `ini:"ENABLE_IONOS"`
	EnableCloudflare          bool   `ini:"ENABLE_CLOUDFLARE"`
}

// ZoneEntry is one zone and the A records to update in it.
type ZoneEntry struct {
	ZoneID    string
	RecordIDs []string
	// Credential is the API token (Cloudflare) or API key (IONOS) used for
	// this zone: the per-zone override if set, else the provider default.
	Credential string
}

// CFConfig maps the [CF] section.
type CFConfig struct {
	CFToken      string `ini:"CF_TOKEN"`
	CFZones      string `ini:"CF_ZONES"`       // raw value: zone:rec1,rec2;zone2:rec3
	CFZoneTokens string `ini:"CF_ZONE_TOKENS"` // raw value: zone:token;zone2:token2
	CFAccountID  string `ini:"CF_ACCOUNT_ID"`

	// Legacy single-zone keys, still accepted: they add one more zone.
	CFZoneID     string `ini:"CF_ZONE_ID"`
	CFDNSEntries string `ini:"CF_DNS_ENTRIES_ID"`

	Zones []ZoneEntry `ini:"-"` // built by ValidateConfig from the keys above
}

// IONOSConfig maps the [IONOS] section.
type IONOSConfig struct {
	IONOSApiUrl      string `ini:"IONOS_API_URL"`
	IONOSApiKey      string `ini:"IONOS_API_KEY"`
	IONOSZones       string `ini:"IONOS_ZONES"`         // raw value: zone:rec1,rec2;zone2:rec3
	IONOSZoneApiKeys string `ini:"IONOS_ZONE_API_KEYS"` // raw value: zone:key;zone2:key2

	// Legacy single-zone keys, still accepted: they add one more zone.
	IONOSZoneID  string `ini:"IONOS_ZONE_ID"`
	IONOSEntries string `ini:"IONOS_DNS_ENTRIES_ID"`

	Zones []ZoneEntry `ini:"-"` // built by ValidateConfig from the keys above
}

// LoadConfig reads the config.ini file at the given path and returns a Config struct
func LoadConfig(path string) (*Config, error) {
	logger.Log("LoadConfig path:", path)
	raw, err := os.ReadFile(path)
	if err != nil {
		logger.Log("Fail to read file:", err)
		return nil, err
	}
	if err := checkZoneSeparators(raw); err != nil {
		return nil, err
	}
	// ';' separates zones, so only " ;" and " #" start an inline comment.
	cfg, err := ini.LoadSources(ini.LoadOptions{SpaceBeforeInlineComment: true}, raw)
	if err != nil {
		logger.Log("Fail to parse file:", err)
		return nil, err
	}

	config := new(Config)
	if err := cfg.MapTo(config); err != nil {
		logger.Log("Fail to map config:", err)
		return nil, err
	}

	if config.GoCI.UseIPDiscovery && strings.TrimSpace(config.GoCI.IPDiscoveryURL) == "" {
		config.GoCI.IPDiscoveryURL = DefaultIPDiscoveryURL
	}

	if valid, err := ValidateConfig(config); !valid {
		logger.Warn("Config validation failed", "error", err.Error())
		return nil, err
	}

	return config, nil
}

func ValidateConfig(config *Config) (bool, error) {
	if config == nil {
		return false, fmt.Errorf("config is nil")
	}

	isDefault := func(v string) bool {
		n := strings.ToLower(strings.TrimSpace(v))
		if n == "" {
			return true
		}
		return strings.HasPrefix(n, "your-") || strings.Contains(n, "replace-me")
	}

	// [GoCI]
	if !config.GoCI.EnableIONOS && !config.GoCI.EnableCloudflare {
		return false, fmt.Errorf("at least one provider must be enabled: GoCI.ENABLE_CLOUDFLARE or GoCI.ENABLE_IONOS")
	}
	if config.GoCI.TTLMinutesCheckDNSEntries < 0 {
		return false, fmt.Errorf("GoCI.TTL_MINUTES_CHECK_DNS_ENTRIES must be >= 0")
	}
	if config.GoCI.UseIPDiscovery {
		u, err := url.Parse(strings.TrimSpace(config.GoCI.IPDiscoveryURL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return false, fmt.Errorf("GoCI.IP_DISCOVERY_URL must be an http(s) URL when USE_IP_DISCOVERY=true")
		}
	} else if isDefault(config.GoCI.DomainToResolve) {
		return false, fmt.Errorf("GoCI.DOMAIN_TO_RESOLVE is empty or default")
	}
	if config.GoCI.OverrideNewIP && isDefault(config.GoCI.NewIP) {
		return false, fmt.Errorf("GoCI.NEW_IP is required when OVERRIDE_NEW_IP=true")
	}

	// [CF]
	if config.GoCI.EnableCloudflare {
		zones, err := buildZones(zoneKeys{
			section: "CF", zones: "CF_ZONES", creds: "CF_ZONE_TOKENS", cred: "CF_TOKEN",
			legacyZone: "CF_ZONE_ID", legacyEntries: "CF_DNS_ENTRIES_ID",
		}, config.CF.CFZones, config.CF.CFZoneTokens, config.CF.CFToken,
			config.CF.CFZoneID, config.CF.CFDNSEntries, isDefault)
		if err != nil {
			return false, err
		}
		config.CF.Zones = zones
	}

	// [IONOS]
	if config.GoCI.EnableIONOS {
		if isDefault(config.IONOS.IONOSApiUrl) {
			return false, fmt.Errorf("IONOS.IONOS_API_URL is empty or default")
		}
		zones, err := buildZones(zoneKeys{
			section: "IONOS", zones: "IONOS_ZONES", creds: "IONOS_ZONE_API_KEYS", cred: "IONOS_API_KEY",
			legacyZone: "IONOS_ZONE_ID", legacyEntries: "IONOS_DNS_ENTRIES_ID",
		}, config.IONOS.IONOSZones, config.IONOS.IONOSZoneApiKeys, config.IONOS.IONOSApiKey,
			config.IONOS.IONOSZoneID, config.IONOS.IONOSEntries, isDefault)
		if err != nil {
			return false, err
		}
		config.IONOS.Zones = zones
	}

	return true, nil
}

// parseDNSEntryIDs converts a comma-separated record-id string into []string.
func parseDNSEntryIDs(raw string) []string {
	var entries []string
	for _, part := range strings.Split(raw, ",") {
		if e := strings.TrimSpace(part); e != "" {
			entries = append(entries, e)
		}
	}
	return entries
}

// multiZoneKeys hold ';'-separated lists.
var multiZoneKeys = []string{"CF_ZONES", "CF_ZONE_TOKENS", "IONOS_ZONES", "IONOS_ZONE_API_KEYS"}

// checkZoneSeparators rejects a multi-zone key with a space before ';'.
// ini would read the rest of the line as a comment and silently drop zones.
func checkZoneSeparators(raw []byte) error {
	for n, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || !slices.Contains(multiZoneKeys, strings.TrimSpace(key)) {
			continue
		}
		if strings.Contains(strings.TrimRight(value, " \t\r"), " ;") || strings.Contains(value, "\t;") {
			return fmt.Errorf("line %d: %s: remove the space before ';' (it would start a comment)", n+1, strings.TrimSpace(key))
		}
	}
	return nil
}

// zoneKeys names the ini keys of one provider, for error messages.
type zoneKeys struct {
	section       string
	zones         string // e.g. CF_ZONES
	creds         string // e.g. CF_ZONE_TOKENS
	cred          string // e.g. CF_TOKEN
	legacyZone    string // e.g. CF_ZONE_ID
	legacyEntries string // e.g. CF_DNS_ENTRIES_ID
}

// buildZones merges the multi-zone key and the legacy single-zone keys of a
// provider into one list, with the credential resolved for every zone.
// A zone listed more than once gets the union of its record IDs.
func buildZones(k zoneKeys, zonesRaw, credsRaw, defaultCred, legacyZone, legacyEntries string, isDefault func(string) bool) ([]ZoneEntry, error) {
	parsed, err := parseZones(zonesRaw)
	if err != nil {
		return nil, fmt.Errorf("%s.%s: %w", k.section, k.zones, err)
	}

	if strings.TrimSpace(legacyZone) != "" || strings.TrimSpace(legacyEntries) != "" {
		if isDefault(legacyZone) {
			return nil, fmt.Errorf("%s.%s is empty or default", k.section, k.legacyZone)
		}
		ids := parseDNSEntryIDs(legacyEntries)
		if len(ids) == 0 {
			return nil, fmt.Errorf("%s.%s must contain at least one record id", k.section, k.legacyEntries)
		}
		parsed = append(parsed, ZoneEntry{ZoneID: strings.TrimSpace(legacyZone), RecordIDs: ids})
	}

	var zones []ZoneEntry
	index := make(map[string]int) // zone ID -> position in zones
	for _, z := range parsed {
		if isDefault(z.ZoneID) {
			return nil, fmt.Errorf("%s.%s contains a default zone id", k.section, k.zones)
		}
		i, ok := index[z.ZoneID]
		if !ok {
			i = len(zones)
			index[z.ZoneID] = i
			zones = append(zones, ZoneEntry{ZoneID: z.ZoneID})
		}
		for _, id := range z.RecordIDs {
			if isDefault(id) {
				return nil, fmt.Errorf("%s: zone %s contains a default record id", k.section, z.ZoneID)
			}
			if !slices.Contains(zones[i].RecordIDs, id) {
				zones[i].RecordIDs = append(zones[i].RecordIDs, id)
			}
		}
	}
	if len(zones) == 0 {
		return nil, fmt.Errorf("%s: no zones configured: set %s.%s", k.section, k.section, k.zones)
	}

	creds, err := parseZoneCredentials(credsRaw)
	if err != nil {
		return nil, fmt.Errorf("%s.%s: %w", k.section, k.creds, err)
	}
	for zoneID := range creds {
		if _, ok := index[zoneID]; !ok {
			return nil, fmt.Errorf("%s.%s: zone %s is not configured", k.section, k.creds, zoneID)
		}
	}
	for i := range zones {
		cred, ok := creds[zones[i].ZoneID]
		if !ok {
			cred = strings.TrimSpace(defaultCred)
		}
		if isDefault(cred) {
			return nil, fmt.Errorf("%s.%s is empty or default (needed by zone %s)", k.section, k.cred, zones[i].ZoneID)
		}
		zones[i].Credential = cred
	}
	return zones, nil
}

// parseZones parses "zone:rec1,rec2;zone2:rec3". Credentials are left empty.
func parseZones(raw string) ([]ZoneEntry, error) {
	var zones []ZoneEntry
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		zoneID, records, ok := strings.Cut(part, ":")
		zoneID = strings.TrimSpace(zoneID)
		if !ok || zoneID == "" {
			return nil, fmt.Errorf("%q: expected <zone id>:<record id>[,<record id>...]", part)
		}
		ids := parseDNSEntryIDs(records)
		if len(ids) == 0 {
			return nil, fmt.Errorf("zone %s has no record ids", zoneID)
		}
		zones = append(zones, ZoneEntry{ZoneID: zoneID, RecordIDs: ids})
	}
	return zones, nil
}

// parseZoneCredentials parses "zone:credential;zone2:credential2".
func parseZoneCredentials(raw string) (map[string]string, error) {
	creds := make(map[string]string)
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		zoneID, cred, ok := strings.Cut(part, ":")
		zoneID, cred = strings.TrimSpace(zoneID), strings.TrimSpace(cred)
		if !ok || zoneID == "" || cred == "" {
			// Do not echo part: it contains a secret.
			return nil, fmt.Errorf("expected <zone id>:<credential> entries separated by ';'")
		}
		if _, dup := creds[zoneID]; dup {
			return nil, fmt.Errorf("zone %s listed twice", zoneID)
		}
		creds[zoneID] = cred
	}
	return creds, nil
}
